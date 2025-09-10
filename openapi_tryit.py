#!/usr/bin/env python3
"""
openapi_tryit.py

Generate and execute sample "Try it" requests for an OpenAPI 3.x spec,
optionally routing traffic through an HTTP(S) proxy (e.g., Burp at 127.0.0.1:8080).

Features
- Supports servers, paths, operations, parameters, request bodies.
- Builds example payloads using explicit examples/defaults; falls back to type-based samples.
- Skips unsafe methods (POST/PUT/PATCH/DELETE) by default unless --allow-unsafe is set.
- Optional inclusion of non-required params via --include-optional.
- Auth header injection via --auth-header "Authorization: Bearer TOKEN".
- Tag or method filters, dry-run mode, basic $ref resolution (local only).
- Rate limiting via --rps. Sequential execution for predictability.
- Proxying via --proxy http://127.0.0.1:8080 and TLS verification controls.

Usage
  python3 openapi_tryit.py -i api.yaml --proxy http://127.0.0.1:8080 --allow-unsafe --include-optional \
    --auth-header "Authorization: Bearer X" --rps 5

Dependencies
  pip install requests PyYAML
"""

import argparse
import json
import os
import re
import sys
import time
from datetime import datetime, timedelta
from urllib.parse import urlencode, quote
from typing import Any, Dict, List, Optional, Tuple

try:
    import yaml  # PyYAML
except ImportError:
    print("Missing dependency: PyYAML. Install with: pip install PyYAML", file=sys.stderr)
    sys.exit(2)

try:
    import requests
except ImportError:
    print("Missing dependency: requests. Install with: pip install requests", file=sys.stderr)
    sys.exit(2)


SAFE_METHODS = {"GET", "HEAD", "OPTIONS"}
ALL_METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}


def load_spec(path: str) -> Dict[str, Any]:
    if re.match(r'^https?://', path, flags=re.I):
        resp = requests.get(path, timeout=30)
        resp.raise_for_status()
        text = resp.text
    else:
        with open(path, "r", encoding="utf-8") as f:
            text = f.read()

    if path.lower().endswith((".yaml", ".yml")) or text.lstrip().startswith(("---", "openapi")):
        return yaml.safe_load(text)
    return json.loads(text)


def pick_server(spec: Dict[str, Any]) -> Tuple[str, Dict[str, Any]]:
    servers = spec.get("servers") or []
    if not servers:
        return "http://localhost", {}
    srv = servers[0]
    url = srv.get("url", "http://localhost")
    variables = srv.get("variables", {})
    # Substitute variables with defaults if present
    def repl(match):
        var = match.group(1)
        default = (variables.get(var) or {}).get("default", f"{{{var}}}")
        return str(default)

    url = re.sub(r'\{([^{}]+)\}', repl, url)
    return url.rstrip("/"), srv


def resolve_ref(spec: Dict[str, Any], obj: Dict[str, Any]) -> Dict[str, Any]:
    """Resolve a local $ref recursively. Leaves external refs untouched."""
    visited = set()
    while isinstance(obj, dict) and "$ref" in obj:
        ref = obj["$ref"]
        if not ref.startswith("#/"):
            # External refs not handled; return as-is
            return obj
        if ref in visited:
            # Circular; bail
            return obj
        visited.add(ref)
        # Resolve JSON Pointer
        parts = ref.lstrip("#/").split("/")
        cur = spec
        try:
            for p in parts:
                # Unescape per JSON Pointer
                p = p.replace("~1", "/").replace("~0", "~")
                cur = cur[p]
            obj = cur
        except Exception:
            return obj
    return obj


def coalesce_example(param_or_schema: Dict[str, Any]) -> Optional[Any]:
    # Prefer 'example' field
    if "example" in param_or_schema:
        return param_or_schema["example"]
    # Or first of 'examples'
    exs = param_or_schema.get("examples")
    if isinstance(exs, dict) and exs:
        first = next(iter(exs.values()))
        if isinstance(first, dict) and "value" in first:
            return first["value"]
    # Or default
    if "default" in param_or_schema:
        return param_or_schema["default"]
    return None


def sample_for_schema(spec: Dict[str, Any], schema: Dict[str, Any], depth: int = 0) -> Any:
    if not isinstance(schema, dict):
        return None
    schema = resolve_ref(spec, schema)

    # If there is an explicit example/default
    ex = coalesce_example(schema)
    if ex is not None:
        return ex

    # Enum: pick first value
    if "enum" in schema and schema["enum"]:
        return schema["enum"][0]

    typ = schema.get("type")
    fmt = schema.get("format")

    if typ == "string":
        if fmt == "date-time":
            return datetime.utcnow().isoformat() + "Z"
        if fmt == "date":
            return datetime.utcnow().date().isoformat()
        if fmt == "email":
            return "user@example.com"
        if fmt == "uuid":
            return "123e4567-e89b-12d3-a456-426614174000"
        if fmt == "uri":
            return "https://example.com/resource"
        if fmt == "ipv4":
            return "192.0.2.1"
        if fmt == "ipv6":
            return "2001:db8::1"
        if schema.get("pattern"):
            # Not attempting pattern compliance; return a placeholder
            return "string"
        if "minLength" in schema:
            return "x" * max(1, int(schema["minLength"]))
        return "string"
    if typ == "integer":
        mn = schema.get("minimum", 0)
        return int(mn)
    if typ == "number":
        mn = schema.get("minimum", 0.0)
        return float(mn)
    if typ == "boolean":
        return True
    if typ == "array":
        items = schema.get("items", {})
        return [sample_for_schema(spec, items, depth + 1)]
    if typ == "object" or "properties" in schema or "additionalProperties" in schema:
        out = {}
        props = schema.get("properties", {})
        required = set(schema.get("required", []))
        for k, v in props.items():
            # include required; optional included as placeholders
            out[k] = sample_for_schema(spec, v, depth + 1)
        addl = schema.get("additionalProperties")
        if isinstance(addl, dict):
            out["additionalProp1"] = sample_for_schema(spec, addl, depth + 1)
        return out

    # oneOf/anyOf: pick first
    for key in ("oneOf", "anyOf", "allOf"):
        if key in schema and isinstance(schema[key], list) and schema[key]:
            return sample_for_schema(spec, schema[key][0], depth + 1)

    # Fallback
    return None


def build_request_examples(
    spec: Dict[str, Any],
    op: Dict[str, Any],
    include_optional: bool,
) -> Tuple[Dict[str, Any], Dict[str, Any], Optional[bytes], Optional[str]]:
    """
    Returns: (path_params, query_params, body_bytes, content_type)
    """
    path_params = {}
    query_params = {}
    headers = {}
    cookies = {}
    body_bytes = None
    content_type = None

    # Parameters
    params = (op.get("parameters") or []) + (spec.get("paths", {}).get("parameters") or [])
    for p in params:
        p = resolve_ref(spec, p)
        name = p.get("name")
        if not name:
            continue
        location = p.get("in")
        required = bool(p.get("required", False))
        schema = p.get("schema") or {}
        example = coalesce_example(p)
        if example is None and schema:
            example = sample_for_schema(spec, schema)

        if not required and not include_optional:
            continue

        if location == "path":
            path_params[name] = example if example is not None else "value"
        elif location == "query":
            query_params[name] = example if example is not None else "value"
        elif location == "header":
            headers[name] = str(example if example is not None else "value")
        elif location == "cookie":
            cookies[name] = str(example if example is not None else "value")

    # Request body
    if "requestBody" in op:
        rb = resolve_ref(spec, op["requestBody"])
        required = bool(rb.get("required", False))
        if required or include_optional:
            content = rb.get("content") or {}
            if content:
                # Prefer JSON
                if "application/json" in content:
                    media = content["application/json"]
                    example = coalesce_example(media)
                    if example is None:
                        schema = media.get("schema") or {}
                        example = sample_for_schema(spec, schema)
                    if example is None:
                        example = {}
                    body_bytes = json.dumps(example).encode("utf-8")
                    content_type = "application/json"
                else:
                    # pick first media type
                    mt, media = next(iter(content.items()))
                    content_type = mt
                    # For form data, build key-values
                    if mt in ("application/x-www-form-urlencoded", "multipart/form-data"):
                        schema = media.get("schema") or {}
                        example = coalesce_example(media)
                        if example is None and schema:
                            example = sample_for_schema(spec, schema)
                        if not isinstance(example, dict):
                            example = {"field": "value"}
                        # We'll let requests handle encoding from dict
                        body_bytes = example  # special-case signal
                    else:
                        example = coalesce_example(media)
                        if example is None:
                            schema = media.get("schema") or {}
                            example = sample_for_schema(spec, schema)
                        if example is None:
                            example = ""
                        if isinstance(example, (dict, list)):
                            body_bytes = json.dumps(example).encode("utf-8")
                        elif isinstance(example, (bytes, bytearray)):
                            body_bytes = bytes(example)
                        else:
                            body_bytes = str(example).encode("utf-8")

    return path_params, {"query": query_params, "headers": headers, "cookies": cookies}, body_bytes, content_type


def substitute_path_params(path_template: str, path_params: Dict[str, Any]) -> str:
    def repl(match):
        key = match.group(1)
        return quote(str(path_params.get(key, key)), safe="")
    return re.sub(r'\{([^{}]+)\}', repl, path_template)


def rate_limit_sleep(last_time: List[float], rps: float):
    if rps <= 0:
        return
    min_interval = 1.0 / rps
    now = time.time()
    delta = now - last_time[0]
    if delta < min_interval:
        time.sleep(min_interval - delta)
    last_time[0] = time.time()


def main():
    ap = argparse.ArgumentParser(description="Execute sample 'Try it' requests for an OpenAPI spec via optional proxy.")
    ap.add_argument("-i", "--input", required=True, help="Path or URL to OpenAPI 3.x spec (YAML or JSON).")
    ap.add_argument("--proxy", help="HTTP(S) proxy base URL, e.g. http://127.0.0.1:8080")
    ap.add_argument("--insecure", action="store_true", help="Disable TLS verification (useful with intercepting proxy).")
    ap.add_argument("--ca-cert", help="Custom CA bundle path for TLS verification.")
    ap.add_argument("--allow-unsafe", action="store_true", help="Include POST/PUT/PATCH/DELETE methods.")
    ap.add_argument("--method", action="append", help="Restrict to specific method(s). May be repeated.")
    ap.add_argument("--include-optional", action="store_true", help="Include optional params and bodies.")
    ap.add_argument("--only-defined-examples", action="store_true", help="Only send when explicit example/default exists (skip synthetic).")
    ap.add_argument("--tag", action="append", help="Only include operations with these tag(s). May be repeated.")
    ap.add_argument("--exclude-tag", action="append", help="Exclude operations with these tag(s). May be repeated.")
    ap.add_argument("--auth-header", action="append", help="Add header(s), e.g. 'Authorization: Bearer TOKEN'. May be repeated.")
    ap.add_argument("--rps", type=float, default=2.0, help="Rate limit requests per second (default: 2).")
    ap.add_argument("--dry-run", action="store_true", help="Print what would be sent without executing.")
    ap.add_argument("--timeout", type=float, default=20.0, help="HTTP request timeout in seconds.")
    args = ap.parse_args()

    spec = load_spec(args.input)

    base_url, _ = pick_server(spec)
    print(f"[INFO] Base server: {base_url}")

    proxies = None
    if args.proxy:
        proxies = {"http": args.proxy, "https": args.proxy}
        print(f"[INFO] Using proxy: {args.proxy}")

    verify = True
    if args.insecure:
        verify = False
        print("[WARN] TLS verification disabled (--insecure).")
    elif args.ca_cert:
        verify = args.ca_cert
        print(f"[INFO] Using custom CA bundle: {args.ca_cert}")

    allowed_methods = set(SAFE_METHODS) if not args.allow_unsafe else set(ALL_METHODS)
    if args.method:
        allowed_methods &= {m.upper() for m in args.method}

    include_tags = set(args.tag or [])
    exclude_tags = set(args.exclude_tag or [])

    extra_headers = {}
    if args.auth_header:
        for h in args.auth_header:
            if ":" not in h:
                print(f"[WARN] Skipping malformed header (missing ':'): {h}")
                continue
            k, v = h.split(":", 1)
            extra_headers[k.strip()] = v.strip()

    session = requests.Session()
    session.proxies = proxies or {}
    session.verify = verify
    session.headers.update({"User-Agent": "openapi-tryit/1.0"})
    last_time = [0.0]

    paths = spec.get("paths") or {}
    total = 0
    sent = 0
    skipped = 0

    for path, path_item in paths.items():
        if not isinstance(path_item, dict):
            continue
        for method, op in path_item.items():
            if method.lower() not in ("get", "post", "put", "patch", "delete", "head", "options"):
                continue
            method_u = method.upper()
            total += 1
            if method_u not in allowed_methods:
                skipped += 1
                continue

            # Tag filters
            tags = set((op.get("tags") or []))
            if include_tags and not (tags & include_tags):
                skipped += 1
                continue
            if exclude_tags and (tags & exclude_tags):
                skipped += 1
                continue

            # Build request examples
            path_params, pack, body_bytes, content_type = build_request_examples(spec, op, include_optional=args.include_optional)

            # Skip if only-defined-examples and we synthesized nothing
            if args.only_defined_examples:
                def had_defined_examples(op: Dict[str, Any]) -> bool:
                    if op.get("parameters"):
                        for p in op["parameters"]:
                            p_res = resolve_ref(spec, p)
                            if coalesce_example(p_res) is not None:
                                return True
                            if p_res.get("schema") and coalesce_example(p_res["schema"]) is not None:
                                return True
                    rb = op.get("requestBody")
                    if rb:
                        rb = resolve_ref(spec, rb)
                        content = rb.get("content") or {}
                        for mt, media in content.items():
                            if coalesce_example(media) is not None:
                                return True
                            if media.get("schema") and coalesce_example(media["schema"]) is not None:
                                return True
                    return False
                if not had_defined_examples(op):
                    print(f"[SKIP] {method_u} {path} (no defined examples/defaults)")
                    skipped += 1
                    continue

            url_path = substitute_path_params(path, path_params)
            url = f"{base_url}{url_path}"

            # Prepare request
            headers = dict(pack["headers"])
            headers.update(extra_headers)

            cookies = dict(pack["cookies"])

            data = None
            json_payload = None
            files = None

            if body_bytes is not None:
                # If body_bytes is a dict signal for form-enc/multipart
                if isinstance(body_bytes, dict):
                    if content_type == "multipart/form-data":
                        # Convert simple fields to files/tuples if necessary
                        files = {}
                        data = {}
                        for k, v in body_bytes.items():
                            if isinstance(v, (bytes, bytearray)):
                                files[k] = ("blob.bin", bytes(v))
                            else:
                                data[k] = v if isinstance(v, str) else json.dumps(v)
                    else:
                        data = body_bytes
                else:
                    # Raw bytes; prefer json if content-type is json and parseable
                    if content_type == "application/json":
                        try:
                            json_payload = json.loads(body_bytes.decode("utf-8"))
                        except Exception:
                            json_payload = None
                            data = body_bytes
                    else:
                        data = body_bytes

                if content_type and "Content-Type" not in headers and files is None:
                    headers["Content-Type"] = content_type

            # Apply query params
            query_params = pack["query"] or {}
            if query_params:
                # requests will encode dicts if passed as params=
                params_arg = query_params
            else:
                params_arg = None

            # Report
            opid = op.get("operationId", "")
            summ = op.get("summary", "")
            print(f"[{method_u}] {url}  tags={list(tags) or '-'}  operationId={opid or '-'}  summary={summ or '-'}")

            if args.dry_run:
                print("  headers:", headers or "-")
                print("  cookies:", cookies or "-")
                print("  params :", params_arg or "-")
                if json_payload is not None:
                    print("  json   :", json.dumps(json_payload)[:400])
                elif data is not None:
                    if isinstance(data, (bytes, bytearray)):
                        preview = data[:200]
                        try:
                            preview = preview.decode("utf-8", errors="replace")
                        except Exception:
                            preview = str(preview)
                        print("  data   :", preview)
                    else:
                        print("  data   :", str(data)[:400])
                if files:
                    print("  files  :", list(files.keys()))
                continue

            # Rate limit
            rate_limit_sleep(last_time, args.rps)

            try:
                resp = session.request(
                    method_u,
                    url,
                    headers=headers,
                    cookies=cookies,
                    params=params_arg,
                    data=None if json_payload is not None else data,
                    json=json_payload,
                    files=files,
                    timeout=args.timeout,
                    allow_redirects=False
                )
                print(f"  -> {resp.status_code} {resp.reason}  len={len(resp.content)}")
            except requests.RequestException as e:
                print(f"  !! Request failed: {e}")
            sent += 1

    print(f"\n[DONE] total operations seen: {total} | attempted: {sent} | skipped: {skipped}")
    if not args.allow_unsafe:
        print("[NOTE] Unsafe methods were skipped. Use --allow-unsafe to include POST/PUT/PATCH/DELETE.")


if __name__ == "__main__":
    main()
