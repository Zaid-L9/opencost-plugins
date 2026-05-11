package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	newrelicplugin "github.com/opencost/opencost-plugins/pkg/plugins/newrelic/newrelicplugin"
)

func TestBuildUsageNRQL(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	got := buildDataIngestNRQL(start, end)
	for _, want := range []string{
		"FROM NrConsumption",
		"sum(GigabytesIngested) AS 'usageQuantity'",
		"productLine = 'DataPlatform'",
		"SINCE '2026-05-01 00:00:00 UTC'",
		"UNTIL '2026-05-02 00:00:00 UTC'",
		"FACET usageMetric",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("query missing %q: %s", want, got)
		}
	}
}

func TestBuildComputeNRQL(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	got := buildComputeNRQL(start, end, "CoreCCU")
	for _, want := range []string{
		"FROM NrConsumption",
		"sum(consumption) AS 'usageQuantity'",
		"metric = 'CoreCCU'",
		"FACET dimension_productCapability",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("query missing %q: %s", want, got)
		}
	}
}

func TestBuildSyntheticsNRQL(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	got := buildSyntheticsNRQL(start, end)
	for _, want := range []string{
		"FROM NrDailyUsage",
		"AS 'usageQuantity'",
		"syntheticsTypeLabel != 'Ping'",
		"FACET syntheticsTypeLabel",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("query missing %q: %s", want, got)
		}
	}
}

func TestCustomCostsFromUsageResults(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	results := []map[string]any{
		{
			"usageMetric":   "MetricsBytes",
			"usageQuantity": 12.5,
		},
		{
			"facet":         "LogsBytes",
			"usageQuantity": 0.0,
		},
	}
	usageQuery := newRelicUsageQuery{
		name:         "data-ingest",
		price:        0.30,
		usageUnit:    "GB",
		resourceType: "Data Ingest",
		facetKey:     "usageMetric",
	}

	costs := customCostsFromUsageResults(results, start, end, 1234567, usageQuery)
	if len(costs) != 1 {
		t.Fatalf("expected one non-zero cost, got %d", len(costs))
	}

	cost := costs[0]
	if cost.ResourceName != "MetricsBytes" {
		t.Fatalf("unexpected resource name: %s", cost.ResourceName)
	}
	if cost.UsageQuantity != 12.5 {
		t.Fatalf("unexpected usage quantity: %f", cost.UsageQuantity)
	}
	if cost.BilledCost != 3.75 {
		t.Fatalf("unexpected billed cost: %f", cost.BilledCost)
	}
	if cost.ProviderId != "newrelic/1234567/data-ingest/MetricsBytes" {
		t.Fatalf("unexpected provider id: %s", cost.ProviderId)
	}
	if cost.ExtendedAttributes == nil || cost.ExtendedAttributes.AccountId == nil || *cost.ExtendedAttributes.AccountId != "1234567" {
		t.Fatalf("expected account id extended attribute, got %#v", cost.ExtendedAttributes)
	}
}

func TestGetNewRelicConfigDefaultsEUEndpoint(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "newrelic-config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"new_relic_api_key": "test-key",
		"account_id": 123,
		"region": "eu",
		"data_ingest_price_per_gb": 0.30
	}`), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	config, err := getNewRelicConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected config error: %v", err)
	}
	if config.NerdGraphAPIURL != defaultEUNerdGraphURL {
		t.Fatalf("expected EU NerdGraph URL, got %s", config.NerdGraphAPIURL)
	}
	if config.LogLevel != "info" {
		t.Fatalf("expected default log level, got %s", config.LogLevel)
	}
}

func TestGetNewRelicConfigRequiresAccountID(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "newrelic-config.json")
	if err := os.WriteFile(configPath, []byte(`{"new_relic_api_key": "test-key"}`), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	_, err := getNewRelicConfig(configPath)
	if err == nil || !strings.Contains(err.Error(), "account_id is required") {
		t.Fatalf("expected account_id validation error, got %v", err)
	}
}

func TestGetNewRelicConfigRequiresAtLeastOnePositiveUnitPrice(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "newrelic-config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"new_relic_api_key": "test-key",
		"account_id": 123
	}`), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	_, err := getNewRelicConfig(configPath)
	if err == nil || !strings.Contains(err.Error(), "at least one New Relic unit price must be greater than 0") {
		t.Fatalf("expected unit price validation error, got %v", err)
	}
}

func TestUsageQueriesIncludeConfiguredPrices(t *testing.T) {
	source := NewRelicCostSource{config: &newrelicplugin.NewRelicConfig{
		DataIngestPriceGB:       0.30,
		CoreCCUPrice:            0.05,
		SyntheticsPricePerCheck: 0.001,
	}}
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	queries := source.usageQueries(start, end)
	if len(queries) != 3 {
		t.Fatalf("expected three configured usage queries, got %d", len(queries))
	}
	if queries[0].name != "data-ingest" || queries[1].name != "core-ccu" || queries[2].name != "synthetics" {
		t.Fatalf("unexpected query order: %#v", queries)
	}
}
