// Copyright (c) 2026 Matthew Gall <me@matthewgall.dev>
//
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const fuelFinderURL = "https://www.fuel-finder.service.gov.uk/internal/v1.0.2/csv/get-latest-fuel-prices-csv"

func main() {
	outPath := flag.String("out", getEnvDefault("FUEL_OUT", "data.csv"), "output path for CSV data")
	outputPath := flag.String("output", "", "output path for CSV data")
	format := flag.String("format", getEnvDefault("FUEL_FORMAT", "csv"), "output format: csv or json")
	flag.Parse()

	if *outputPath != "" {
		*outPath = *outputPath
	}

	if *format == "json" && *outPath == "data.csv" {
		*outPath = "data.json"
	}

	if *outPath == "" {
		exitWithError(errors.New("output path cannot be empty"))
	}

	if *format != "csv" && *format != "json" {
		exitWithError(fmt.Errorf("unsupported format: %s", *format))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	payload, err := fetchFuelData(client)
	if err != nil {
		exitWithError(err)
	}

	if err := validateCSV(payload); err != nil {
		exitWithError(fmt.Errorf("invalid CSV: %w", err))
	}

	if *format == "json" {
		jsonPayload, err := convertCSVToJSON(payload)
		if err != nil {
			exitWithError(fmt.Errorf("convert to JSON: %w", err))
		}
		if err := os.WriteFile(*outPath, jsonPayload, 0o644); err != nil {
			exitWithError(fmt.Errorf("write output: %w", err))
		}
		return
	}

	if err := os.WriteFile(*outPath, payload, 0o644); err != nil {
		exitWithError(fmt.Errorf("write output: %w", err))
	}
}

func validateCSV(payload []byte) error {
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = -1
	for {
		_, err := reader.Read()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func fetchFuelData(client *http.Client) ([]byte, error) {
	var lastErr error
	for _, target := range buildFuelFinderTargets() {
		payload, err := fetchFuelDataFromURL(client, target)
		if err != nil {
			lastErr = err
			continue
		}
		if len(payload) == 0 {
			lastErr = errors.New("received empty response")
			continue
		}
		return payload, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("failed to fetch fuel data")
}

func buildFuelFinderTargets() []string {
	proxyTemplate := strings.TrimSpace(os.Getenv("FUEL_PROXY_TEMPLATE"))
	if proxyTemplate == "" {
		return []string{fuelFinderURL}
	}

	proxyURL := buildProxyURL(proxyTemplate, fuelFinderURL)
	return []string{fuelFinderURL, proxyURL}
}

func buildProxyURL(template, target string) string {
	if strings.Contains(template, "{url}") {
		return strings.ReplaceAll(template, "{url}", url.QueryEscape(target))
	}
	return template + target
}

func fetchFuelDataFromURL(client *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/csv,application/octet-stream;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	req.Header.Set("Referer", "https://www.gov.uk/guidance/access-fuel-price-data")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch fuel data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return payload, nil
}

func convertCSVToJSON(payload []byte) ([]byte, error) {
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(header) == 0 {
		return nil, errors.New("missing header row")
	}

	var records []map[string]any
	for {
		row, err := reader.Read()
		if err == nil {
			if len(row) != len(header) {
				return nil, fmt.Errorf("row has %d fields, expected %d", len(row), len(header))
			}
			entry := make(map[string]any, len(header))
			for i, key := range header {
				value, err := normalizeValue(key, row[i])
				if err != nil {
					return nil, fmt.Errorf("parse %s: %w", key, err)
				}
				if err := setNestedValue(entry, strings.Split(key, "."), value); err != nil {
					return nil, fmt.Errorf("set %s: %w", key, err)
				}
			}
			records = append(records, entry)
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return nil, err
	}

	return json.MarshalIndent(records, "", "  ")
}

func normalizeValue(key, raw string) (any, error) {
	if raw == "" {
		if isNullableNumericField(key) {
			return nil, nil
		}
		return "", nil
	}

	if isNullableNumericField(key) {
		value, err := parseFloat(raw)
		if err != nil {
			return nil, err
		}
		return value, nil
	}

	if raw == "true" || raw == "false" {
		value, err := parseBool(raw)
		if err != nil {
			return nil, err
		}
		return value, nil
	}

	return raw, nil
}

func isNullableNumericField(key string) bool {
	if key == "forecourts.location.latitude" || key == "forecourts.location.longitude" {
		return true
	}
	return strings.HasPrefix(key, "forecourts.fuel_price.")
}

func parseFloat(raw string) (float64, error) {
	return strconv.ParseFloat(raw, 64)
}

func parseBool(raw string) (bool, error) {
	return strconv.ParseBool(raw)
}

func setNestedValue(root map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return errors.New("empty key path")
	}

	current := root
	for i := 0; i < len(path)-1; i++ {
		segment := path[i]
		if segment == "" {
			return errors.New("empty key segment")
		}
		if next, ok := current[segment]; ok {
			nested, ok := next.(map[string]any)
			if !ok {
				return fmt.Errorf("%s is not an object", strings.Join(path[:i+1], "."))
			}
			current = nested
			continue
		}
		child := make(map[string]any)
		current[segment] = child
		current = child
	}

	leaf := path[len(path)-1]
	if leaf == "" {
		return errors.New("empty key segment")
	}
	current[leaf] = value
	return nil
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func getEnvDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return value
}
