# New Relic Plugin

The New Relic plugin queries New Relic NerdGraph for billing-related usage data
and returns OpenCost custom costs. It can report data ingest, core compute,
advanced compute, and synthetics usage.

New Relic usage query results are approximate and may not exactly match an
invoice. Set one or more unit price fields to the rates you want OpenCost to
apply when converting usage into costs.

## Configuration

```json
{
  "new_relic_api_key": "NRAK-...",
  "account_id": 1234567,
  "region": "us",
  "data_ingest_price_per_gb": 0.30,
  "core_ccu_price": 0.05,
  "advanced_ccu_price": 0.10,
  "synthetics_price_per_check": 0.001,
  "custom_usage_queries": [
    {
      "name": "cloud",
      "nrql": "FROM NrConsumption SELECT sum(consumption) AS 'usageQuantity' SINCE '{{start}}' UNTIL '{{end}}' FACET productLine LIMIT MAX",
      "unit_price": 0.42,
      "usage_unit": "units",
      "resource_type": "Cloud Usage",
      "facet_key": "productLine"
    }
  ],
  "log_level": "info"
}
```

For EU accounts, set `"region": "eu"`. To override the NerdGraph endpoint
directly, set `nerdgraph_api_url`. At least one unit price must be greater than
zero. Any unit price left as `0` is skipped.

Use `custom_usage_queries` for plan-specific New Relic usage lines that are not
covered by the defaults. Each custom query must return a numeric
`usageQuantity`; the plugin multiplies it by `unit_price`. The optional
`{{start}}` and `{{end}}` placeholders render as the current OpenCost request
window in New Relic's NRQL date format.

## Data Source

The plugin runs a NerdGraph NRQL query like:

```sql
FROM NrConsumption
SELECT sum(GigabytesIngested) AS 'usageQuantity'
WHERE productLine = 'DataPlatform'
SINCE '<window-start>'
UNTIL '<window-end>'
FACET usageMetric
LIMIT MAX
```

```sql
FROM NrConsumption
SELECT sum(consumption) AS 'usageQuantity'
WHERE metric = 'CoreCCU'
SINCE '<window-start>'
UNTIL '<window-end>'
FACET dimension_productCapability
LIMIT MAX
```

```sql
FROM NrConsumption
SELECT sum(consumption) AS 'usageQuantity'
WHERE metric = 'AdvancedCCU'
SINCE '<window-start>'
UNTIL '<window-end>'
FACET dimension_productCapability
LIMIT MAX
```

```sql
FROM NrDailyUsage
SELECT (sum(syntheticsFailedCheckCount) + sum(syntheticsSuccessCheckCount)) AS 'usageQuantity'
WHERE syntheticsTypeLabel != 'Ping'
SINCE '<window-start>'
UNTIL '<window-end>'
FACET syntheticsTypeLabel
LIMIT MAX
```
