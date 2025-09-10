# wpswag

A utility to read a WordPress REST definition and generate an OpenAPI/Swagger v3 spec.

## Usage

```
./wpswag -h
Usage of ./wpswag:
  -debug
        Print debug stats to stderr
  -o string
        Output OpenAPI file (defaults to stdout)
  -u string
        WordPress REST URL or local JSON file (e.g. https://site/wp-json or ./wp-json.json)
```

For example:

`./wpswag -u https://wptavern.com/wp-json -o wptavern.json`

## Installing

* Clone the repository
* `go build`

## Viewing

Use swagger-ui to view the resulting file. The easiest way to do this is using docker and a command like this:

```
docker run --rm -p 8080:8080 \
  -e SWAGGER_JSON=/spec/<RESULTING_FILE>.json \
  -v "$(pwd)":/spec \
  swaggerapi/swagger-ui
```

This will create a web UI on http://127.0.0.1:8080/ that you can use to view and interact with all of the methods.

## What it does

If you hit either `/wp-json` or `/?rest-route=/` on a WordPress site, you'll get a big JSON blob explaining every registered REST route on the WordPress instance. This will include core WordPress methods like `/wp-json/wp/v2/users` as well as those created by plugins.

The blob will include the path, HTTP VERB and argument details for each method. Which looks a lot like a swagger file. So let's make it one.

## Enum

There's also a tool for consuming the OpenAPI spec and making every one of the requests constained with in (akin to clicking Try It -> Execute for each method in the swagger file). This can help you see what can be accessed en masse, as most methods aren't accessivble without authentication. Run it through your Burp proxy for extra insight.

```
 python3 openapi_tryit.py -h
usage: openapi_tryit.py [-h] -i INPUT [--proxy PROXY] [--insecure] [--ca-cert CA_CERT] [--allow-unsafe]
                        [--method METHOD] [--include-optional] [--only-defined-examples] [--tag TAG]
                        [--exclude-tag EXCLUDE_TAG] [--auth-header AUTH_HEADER] [--rps RPS] [--dry-run]
                        [--timeout TIMEOUT]

Execute sample 'Try it' requests for an OpenAPI spec via optional proxy.

options:
  -h, --help            show this help message and exit
  -i, --input INPUT     Path or URL to OpenAPI 3.x spec (YAML or JSON).
  --proxy PROXY         HTTP(S) proxy base URL, e.g. http://127.0.0.1:8080
  --insecure            Disable TLS verification (useful with intercepting proxy).
  --ca-cert CA_CERT     Custom CA bundle path for TLS verification.
  --allow-unsafe        Include POST/PUT/PATCH/DELETE methods.
  --method METHOD       Restrict to specific method(s). May be repeated.
  --include-optional    Include optional params and bodies.
  --only-defined-examples
                        Only send when explicit example/default exists (skip synthetic).
  --tag TAG             Only include operations with these tag(s). May be repeated.
  --exclude-tag EXCLUDE_TAG
                        Exclude operations with these tag(s). May be repeated.
  --auth-header AUTH_HEADER
                        Add header(s), e.g. 'Authorization: Bearer TOKEN'. May be repeated.
  --rps RPS             Rate limit requests per second (default: 2).
  --dry-run             Print what would be sent without executing.
  --timeout TIMEOUT     HTTP request timeout in seconds.
```

For example:

`python3 openapi_tryit.py -i wptavern.json --proxy http://127.0.0.1:8080 --insecure --rps 10 --allow-unsafe`
