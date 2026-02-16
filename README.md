# Fuel Finder Archive

Fetches the latest UK Fuel Finder CSV data on a schedule and stores it in this repository.

## Usage

Fetch and write CSV data:

```bash
go run .
```

Output to a different path:

```bash
go run . -out path/to/data.csv
```

Use the long form flag:

```bash
go run . -output path/to/data.csv
```

Write JSON with nested keys:

```bash
go run . -format json
```

### Environment variables

- `FUEL_OUT`: default output path (overridden by `-out`)
- `FUEL_FORMAT`: default output format (`csv` or `json`, overridden by `-format`)
- `FUEL_PROXY_TEMPLATE`: optional fallback proxy template for the fuel URL; use `{url}` placeholder for a query parameter template, or provide a prefix to append the target URL.

## GitHub Action

The workflow runs hourly and on manual dispatch, committing `data.csv` only when changes are detected.
