# wpswag

`wpswag` converts a WordPress REST API into an OpenAPI 3.0.3 JSON file.

## Build

```bash
go build -o wpswag .
```

## Usage

```bash
./wpswag convert -u "https://example.com/wp-json" -o openapi.json
```

You can also run it without building:

```bash
go run . convert -u "https://example.com/wp-json"
```

## Notes

- Input can be a WordPress REST URL or a local JSON file.
- Output defaults to `openapi.json`.
- The generated spec includes typed schemas for core `wp/v2` resources when schema metadata is available.
