package newrelicplugin

type NewRelicConfig struct {
	APIKey                  string                     `json:"new_relic_api_key"`
	AccountID               int                        `json:"account_id"`
	Region                  string                     `json:"region"`
	LogLevel                string                     `json:"log_level"`
	DataIngestPriceGB       float64                    `json:"data_ingest_price_per_gb"`
	CoreCCUPrice            float64                    `json:"core_ccu_price"`
	AdvancedCCUPrice        float64                    `json:"advanced_ccu_price"`
	SyntheticsPricePerCheck float64                    `json:"synthetics_price_per_check"`
	NerdGraphAPIURL         string                     `json:"nerdgraph_api_url"`
	CustomUsageQueries      []NewRelicCustomUsageQuery `json:"custom_usage_queries"`
}

type NewRelicCustomUsageQuery struct {
	Name         string  `json:"name"`
	NRQL         string  `json:"nrql"`
	UnitPrice    float64 `json:"unit_price"`
	UsageUnit    string  `json:"usage_unit"`
	ResourceType string  `json:"resource_type"`
	FacetKey     string  `json:"facet_key"`
}
