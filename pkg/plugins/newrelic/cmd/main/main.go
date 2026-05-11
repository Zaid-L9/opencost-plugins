package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-plugin"
	commonconfig "github.com/opencost/opencost-plugins/pkg/common/config"
	newrelicplugin "github.com/opencost/opencost-plugins/pkg/plugins/newrelic/newrelicplugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	ocplugin "github.com/opencost/opencost/core/pkg/plugin"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PLUGIN_NAME",
	MagicCookieValue: "newrelic",
}

const (
	defaultUSNerdGraphURL = "https://api.newrelic.com/graphql"
	defaultEUNerdGraphURL = "https://api.eu.newrelic.com/graphql"
	newRelicDateFormat    = "2006-01-02 15:04:05 MST"
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type NewRelicCostSource struct {
	client      HTTPClient
	rateLimiter *rate.Limiter
	config      *newrelicplugin.NewRelicConfig
}

type newRelicUsageQuery struct {
	name         string
	nrql         string
	price        float64
	usageUnit    string
	resourceType string
	facetKey     string
}

func (n *NewRelicCostSource) GetCustomCosts(req *pb.CustomCostRequest) []*pb.CustomCostResponse {
	results := []*pb.CustomCostResponse{}

	targets, err := opencost.GetWindows(req.Start.AsTime(), req.End.AsTime(), req.Resolution.AsDuration())
	if err != nil {
		log.Errorf("error getting windows: %v", err)
		return append(results, &pb.CustomCostResponse{Errors: []string{fmt.Sprintf("error getting windows: %v", err)}})
	}

	for _, target := range targets {
		if target.Start().After(time.Now().UTC()) {
			log.Debugf("skipping future window %v", target)
			continue
		}

		results = append(results, n.getNewRelicCostsForWindow(target))
	}

	return results
}

func main() {
	configFile, err := commonconfig.GetConfigFilePath()
	if err != nil {
		log.Fatalf("error opening config file: %v", err)
	}

	nrConfig, err := getNewRelicConfig(configFile)
	if err != nil {
		log.Fatalf("error building New Relic config: %v", err)
	}
	log.SetLogLevel(nrConfig.LogLevel)

	nrCostSrc := NewRelicCostSource{
		client:      &http.Client{Timeout: 30 * time.Second},
		rateLimiter: rate.NewLimiter(0.5, 1),
		config:      nrConfig,
	}

	pluginMap := map[string]plugin.Plugin{
		"CustomCostSource": &ocplugin.CustomCostPlugin{Impl: &nrCostSrc},
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}

func boilerplateNewRelicCustomCost(win opencost.Window) pb.CustomCostResponse {
	return pb.CustomCostResponse{
		Metadata:   map[string]string{"api_client": "nerdgraph"},
		CostSource: "observability",
		Domain:     "newrelic",
		Version:    "v1",
		Currency:   "USD",
		Start:      timestamppb.New(*win.Start()),
		End:        timestamppb.New(*win.End()),
		Errors:     []string{},
		Costs:      []*pb.CustomCost{},
	}
}

func (n *NewRelicCostSource) getNewRelicCostsForWindow(window opencost.Window) *pb.CustomCostResponse {
	ccResp := boilerplateNewRelicCustomCost(window)

	for _, usageQuery := range n.usageQueries(*window.Start(), *window.End()) {
		results, err := n.queryNewRelicUsage(usageQuery)
		if err != nil {
			ccResp.Errors = append(ccResp.Errors, fmt.Sprintf("error querying New Relic %s usage: %v", usageQuery.name, err))
			continue
		}

		ccResp.Costs = append(ccResp.Costs, customCostsFromUsageResults(results, *window.Start(), *window.End(), n.config.AccountID, usageQuery)...)
	}

	return &ccResp
}

func (n *NewRelicCostSource) usageQueries(start, end time.Time) []newRelicUsageQuery {
	queries := []newRelicUsageQuery{}

	if n.config.DataIngestPriceGB > 0 {
		queries = append(queries, newRelicUsageQuery{
			name:         "data-ingest",
			nrql:         buildDataIngestNRQL(start, end),
			price:        n.config.DataIngestPriceGB,
			usageUnit:    "GB",
			resourceType: "Data Ingest",
			facetKey:     "usageMetric",
		})
	}
	if n.config.CoreCCUPrice > 0 {
		queries = append(queries, newRelicUsageQuery{
			name:         "core-ccu",
			nrql:         buildComputeNRQL(start, end, "CoreCCU"),
			price:        n.config.CoreCCUPrice,
			usageUnit:    "CCU",
			resourceType: "Core Compute",
			facetKey:     "dimension_productCapability",
		})
	}
	if n.config.AdvancedCCUPrice > 0 {
		queries = append(queries, newRelicUsageQuery{
			name:         "advanced-ccu",
			nrql:         buildComputeNRQL(start, end, "AdvancedCCU"),
			price:        n.config.AdvancedCCUPrice,
			usageUnit:    "CCU",
			resourceType: "Advanced Compute",
			facetKey:     "dimension_productCapability",
		})
	}
	if n.config.SyntheticsPricePerCheck > 0 {
		queries = append(queries, newRelicUsageQuery{
			name:         "synthetics",
			nrql:         buildSyntheticsNRQL(start, end),
			price:        n.config.SyntheticsPricePerCheck,
			usageUnit:    "checks",
			resourceType: "Synthetics",
			facetKey:     "syntheticsTypeLabel",
		})
	}

	return queries
}

func (n *NewRelicCostSource) queryNewRelicUsage(usageQuery newRelicUsageQuery) ([]map[string]any, error) {
	if err := n.rateLimiter.Wait(context.Background()); err != nil {
		return nil, fmt.Errorf("error waiting for rate limiter: %v", err)
	}

	graphQLQuery := fmt.Sprintf(`{ actor { account(id: %d) { nrql(query: %q) { results } } } }`, n.config.AccountID, usageQuery.nrql)
	reqBody, err := json.Marshal(map[string]string{"query": graphQLQuery})
	if err != nil {
		return nil, fmt.Errorf("error marshaling NerdGraph request: %v", err)
	}

	req, err := http.NewRequest("POST", n.config.NerdGraphAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("error creating NerdGraph request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Key", n.config.APIKey)

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error calling NerdGraph: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading NerdGraph response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 response from NerdGraph: %d body: %s", resp.StatusCode, string(body))
	}

	var decoded nerdGraphUsageResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("error decoding NerdGraph response: %v", err)
	}
	if len(decoded.Errors) > 0 {
		return nil, fmt.Errorf("NerdGraph returned errors: %s", decoded.errorMessages())
	}

	return decoded.Data.Actor.Account.Nrql.Results, nil
}

func buildDataIngestNRQL(start, end time.Time) string {
	return fmt.Sprintf(
		"FROM NrConsumption SELECT sum(GigabytesIngested) AS 'usageQuantity' WHERE productLine = 'DataPlatform' SINCE '%s' UNTIL '%s' FACET usageMetric LIMIT MAX",
		start.UTC().Format(newRelicDateFormat),
		end.UTC().Format(newRelicDateFormat),
	)
}

func buildComputeNRQL(start, end time.Time, metric string) string {
	return fmt.Sprintf(
		"FROM NrConsumption SELECT sum(consumption) AS 'usageQuantity' WHERE metric = '%s' SINCE '%s' UNTIL '%s' FACET dimension_productCapability LIMIT MAX",
		metric,
		start.UTC().Format(newRelicDateFormat),
		end.UTC().Format(newRelicDateFormat),
	)
}

func buildSyntheticsNRQL(start, end time.Time) string {
	return fmt.Sprintf(
		"FROM NrDailyUsage SELECT (sum(syntheticsFailedCheckCount) + sum(syntheticsSuccessCheckCount)) AS 'usageQuantity' WHERE syntheticsTypeLabel != 'Ping' SINCE '%s' UNTIL '%s' FACET syntheticsTypeLabel LIMIT MAX",
		start.UTC().Format(newRelicDateFormat),
		end.UTC().Format(newRelicDateFormat),
	)
}

func customCostsFromUsageResults(results []map[string]any, start, end time.Time, accountID int, usageQuery newRelicUsageQuery) []*pb.CustomCost {
	customCosts := []*pb.CustomCost{}
	accountIDString := fmt.Sprint(accountID)

	for _, result := range results {
		usageQuantity := numericField(result, "usageQuantity")
		if usageQuantity <= 0 {
			continue
		}

		usageMetric := stringField(result, usageQuery.facetKey)
		if usageMetric == "" {
			usageMetric = stringField(result, "facet")
		}
		if usageMetric == "" {
			usageMetric = usageQuery.name
		}

		billedCost := usageQuantity * usageQuery.price
		extendedAttrs := pb.CustomCostExtendedAttributes{
			AccountId: &accountIDString,
		}
		customCost := pb.CustomCost{
			AccountName:        fmt.Sprintf("New Relic account %d", accountID),
			ChargeCategory:     "Usage",
			Description:        fmt.Sprintf("New Relic %s usage for %s from %s to %s", usageQuery.resourceType, usageMetric, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)),
			ResourceName:       usageMetric,
			ResourceType:       usageQuery.resourceType,
			Id:                 uuid.NewString(),
			ProviderId:         fmt.Sprintf("newrelic/%d/%s/%s", accountID, usageQuery.name, usageMetric),
			BilledCost:         float32(billedCost),
			ListCost:           float32(billedCost),
			ListUnitPrice:      float32(usageQuery.price),
			UsageQuantity:      float32(usageQuantity),
			UsageUnit:          usageQuery.usageUnit,
			Labels:             map[string]string{"newrelicUsageQuery": usageQuery.name},
			ExtendedAttributes: &extendedAttrs,
		}
		customCosts = append(customCosts, &customCost)
	}

	return customCosts
}

func numericField(result map[string]any, preferredKey string) float64 {
	keys := []string{preferredKey, "gigabytesIngested", "sum.GigabytesIngested", "consumption", "sum.consumption", "Total Checks", "sum", "latest.GigabytesIngested", "latest"}
	for _, key := range keys {
		switch value := result[key].(type) {
		case float64:
			return value
		case int:
			return float64(value)
		case json.Number:
			parsed, _ := value.Float64()
			return parsed
		}
	}
	return 0
}

func stringField(result map[string]any, key string) string {
	value, ok := result[key]
	if !ok {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := []string{}
		for _, part := range typed {
			parts = append(parts, fmt.Sprint(part))
		}
		return strings.Join(parts, "/")
	default:
		return fmt.Sprint(typed)
	}
}

type nerdGraphUsageResponse struct {
	Data struct {
		Actor struct {
			Account struct {
				Nrql struct {
					Results []map[string]any `json:"results"`
				} `json:"nrql"`
			} `json:"account"`
		} `json:"actor"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (n nerdGraphUsageResponse) errorMessages() string {
	messages := []string{}
	for _, err := range n.Errors {
		messages = append(messages, err.Message)
	}
	return strings.Join(messages, "; ")
}

func getNewRelicConfig(configFilePath string) (*newrelicplugin.NewRelicConfig, error) {
	var result newrelicplugin.NewRelicConfig
	bytes, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file for New Relic config @ %s: %v", configFilePath, err)
	}
	if err := json.Unmarshal(bytes, &result); err != nil {
		return nil, fmt.Errorf("error unmarshaling New Relic config: %v", err)
	}

	if result.APIKey == "" {
		return nil, fmt.Errorf("new_relic_api_key is required")
	}
	if result.AccountID == 0 {
		return nil, fmt.Errorf("account_id is required")
	}
	if result.DataIngestPriceGB < 0 || result.CoreCCUPrice < 0 || result.AdvancedCCUPrice < 0 || result.SyntheticsPricePerCheck < 0 {
		return nil, fmt.Errorf("New Relic unit prices must be greater than or equal to 0")
	}
	if result.DataIngestPriceGB == 0 && result.CoreCCUPrice == 0 && result.AdvancedCCUPrice == 0 && result.SyntheticsPricePerCheck == 0 {
		return nil, fmt.Errorf("at least one New Relic unit price must be greater than 0")
	}
	if result.LogLevel == "" {
		result.LogLevel = "info"
	}
	if result.NerdGraphAPIURL == "" {
		switch strings.ToLower(result.Region) {
		case "eu":
			result.NerdGraphAPIURL = defaultEUNerdGraphURL
		default:
			result.NerdGraphAPIURL = defaultUSNerdGraphURL
		}
	}

	return &result, nil
}
