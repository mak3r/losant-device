// create-dashboards.go scaffolds the Fleet Overview and Cluster Detail Losant
// dashboards for a k8s-cluster operator deployment. Run with:
//
//	go run scripts/create-dashboards.go --app-id $LOSANT_APP_ID --api-token $LOSANT_API_TOKEN
//
// The script is idempotent: it skips creation when a dashboard with the same
// canonical name already exists (unless --force is set).
//
// Block layout notes:
//   - The Losant dashboard grid is 4 columns wide. Block positions use
//     startX/startY/width/height (not a bounds object).
//   - Custom-chart, custom-html, and pie blocks are created as placeholders
//     with empty data segments; configure their queries in the Losant UI.
//   - Cluster Detail blocks use fleet-level queries. After creation, add a
//     Device ID context variable named "deviceId" in the Losant UI and update
//     each block's query to filter by {{ctx.deviceId}}.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	apiBase           = "https://api.losant.com"
	dashboardUIBase   = "https://app.losant.com/dashboards"
	nameFleetOverview = "k8s-fleet-overview"
	nameClusterDetail = "k8s-cluster-detail"
)

// ---- Vega-Lite specs embedded as raw string literals -------------------------

var specTreemap = `{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "data": {"name": "table"},
  "mark": "rect",
  "encoding": {
    "x": {"field": "total_nodes", "type": "quantitative", "title": "Total Nodes"},
    "color": {
      "field": "health_score",
      "type": "quantitative",
      "scale": {"scheme": "redyellowgreen", "domain": [0, 100]}
    },
    "tooltip": [
      {"field": "deviceName", "type": "nominal", "title": "Cluster"},
      {"field": "health_score", "type": "quantitative", "title": "Health Score"},
      {"field": "total_nodes", "type": "quantitative", "title": "Nodes"}
    ]
  }
}`

var specStripPlotRegion = `{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "data": {"name": "table"},
  "mark": {"type": "point", "filled": true, "size": 80},
  "encoding": {
    "x": {"field": "health_score", "type": "quantitative", "scale": {"domain": [0, 100]}, "title": "Health Score"},
    "y": {"field": "region", "type": "nominal", "title": "Region"},
    "color": {
      "field": "health_score",
      "type": "quantitative",
      "scale": {"scheme": "redyellowgreen", "domain": [0, 100]}
    },
    "tooltip": [
      {"field": "deviceName", "type": "nominal", "title": "Cluster"},
      {"field": "region", "type": "nominal", "title": "Region"},
      {"field": "health_score", "type": "quantitative", "title": "Health Score"}
    ]
  }
}`

var specHeatmap = `{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "data": {"name": "table"},
  "mark": "rect",
  "encoding": {
    "y": {"field": "region", "type": "nominal", "title": "Region"},
    "x": {"field": "metric", "type": "nominal", "title": "Metric"},
    "color": {
      "field": "value",
      "type": "quantitative",
      "scale": {"scheme": "redyellowgreen"}
    },
    "tooltip": [
      {"field": "region", "type": "nominal"},
      {"field": "metric", "type": "nominal"},
      {"field": "value", "type": "quantitative"}
    ]
  }
}`

var specStripStaleness = `{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "data": {"name": "table"},
  "mark": {"type": "point", "filled": true, "size": 80},
  "encoding": {
    "x": {"field": "minutes_since_report", "type": "quantitative", "title": "Minutes Since Last Report"},
    "y": {"field": "region", "type": "nominal", "title": "Region"},
    "color": {
      "field": "minutes_since_report",
      "type": "quantitative",
      "scale": {"scheme": "redyellowgreen", "reverse": true, "domain": [0, 30]}
    },
    "tooltip": [
      {"field": "deviceName", "type": "nominal", "title": "Cluster"},
      {"field": "region", "type": "nominal"},
      {"field": "minutes_since_report", "type": "quantitative", "title": "Minutes Since Report"}
    ]
  }
}`

var htmlRegionTiles = `<!DOCTYPE html>
<html>
<head>
<style>
  .grid { display:flex; flex-wrap:wrap; gap:8px; padding:8px; }
  .tile { flex:1 1 120px; min-height:60px; border-radius:6px; display:flex;
          align-items:center; justify-content:center; color:#fff;
          font-family:sans-serif; font-size:13px; font-weight:600; }
  .healthy { background:#27ae60; }
  .degraded { background:#f39c12; }
  .critical { background:#e74c3c; }
</style>
</head>
<body>
<div class="grid" id="tiles"></div>
<script>
(function () {
  var container = document.getElementById('tiles');
  var devices = (window.losantData && window.losantData.devices) || [];
  var regionWorst = {};
  devices.forEach(function(d) {
    var region = (d.tags && d.tags.region) || 'unknown';
    var score = (d.compositeState && d.compositeState.health_score && d.compositeState.health_score.value) || 0;
    if (!(region in regionWorst) || score < regionWorst[region]) {
      regionWorst[region] = score;
    }
  });
  Object.keys(regionWorst).sort().forEach(function(region) {
    var score = regionWorst[region];
    var cls = score >= 80 ? 'healthy' : score >= 50 ? 'degraded' : 'critical';
    var tile = document.createElement('div');
    tile.className = 'tile ' + cls;
    tile.textContent = region + ' (' + Math.round(score) + ')';
    container.appendChild(tile);
  });
  if (Object.keys(regionWorst).length === 0) {
    container.innerHTML = '<p style="color:#666;padding:12px">No device data</p>';
  }
})();
</script>
</body>
</html>`

// ---- API client ---------------------------------------------------------------

type apiClient struct {
	appID string
	token string
	http  *http.Client
}

func (c *apiClient) do(method, path string, body interface{}) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, path, r)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api %s %s → %d: %s", method, path, resp.StatusCode, b)
	}
	return b, nil
}

func (c *apiClient) findDashboard(name string) (string, error) {
	path := fmt.Sprintf("%s/applications/%s/dashboards?filterField=name&filter=%s",
		apiBase, c.appID, url.QueryEscape(name))
	b, err := c.do("GET", path, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Items) == 0 {
		return "", nil
	}
	return result.Items[0].ID, nil
}

func (c *apiClient) deleteDashboard(id string) error {
	path := fmt.Sprintf("%s/applications/%s/dashboards/%s", apiBase, c.appID, id)
	_, err := c.do("DELETE", path, nil)
	return err
}

func (c *apiClient) createDashboard(payload map[string]interface{}) (string, error) {
	path := fmt.Sprintf("%s/applications/%s/dashboards", apiBase, c.appID)
	b, err := c.do("POST", path, payload)
	if err != nil {
		return "", err
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("empty id in response")
	}
	return result.ID, nil
}

func (c *apiClient) ensureDashboard(name string, payload map[string]interface{}, force bool) (string, error) {
	existing, err := c.findDashboard(name)
	if err != nil {
		return "", fmt.Errorf("find %q: %w", name, err)
	}
	if existing != "" {
		if !force {
			fmt.Printf("  [skip] dashboard %q already exists: %s\n", name, existing)
			return existing, nil
		}
		fmt.Printf("  [force] deleting existing dashboard %q (%s)\n", name, existing)
		if err := c.deleteDashboard(existing); err != nil {
			return "", fmt.Errorf("delete %q: %w", name, err)
		}
	}
	id, err := c.createDashboard(payload)
	if err != nil {
		return "", fmt.Errorf("create %q: %w", name, err)
	}
	fmt.Printf("  [created] %q → %s\n", name, id)
	return id, nil
}

// ---- Block helpers -----------------------------------------------------------

// pos returns startX, startY, width, height fields for a block.
func pos(x, y, w, h int) map[string]interface{} {
	return map[string]interface{}{
		"startX": x,
		"startY": y,
		"width":  w,
		"height": h,
	}
}

// mergeBlock merges position, metadata, and config into a single block map.
func mergeBlock(id, blockType, title, appID string, position map[string]interface{}, config map[string]interface{}) map[string]interface{} {
	b := map[string]interface{}{
		"id":            id,
		"blockType":     blockType,
		"title":         title,
		"applicationId": appID,
		"config":        config,
	}
	for k, v := range position {
		b[k] = v
	}
	return b
}

// deviceQuery encodes a Losant device query as a JSON string.
// Losant block configs expect the query as a serialized JSON string, not an object.
func deviceQuery(filter map[string]interface{}) string {
	b, _ := json.Marshal(filter)
	return string(b)
}

// edgeQuery returns a query string matching all edgeCompute devices.
func edgeQuery() string {
	return deviceQuery(map[string]interface{}{
		"$and": []map[string]interface{}{
			{"deviceClass": map[string]interface{}{"$eq": "edgeCompute"}},
		},
	})
}

// tagQuery returns a query string matching devices with a specific tag value.
func tagQuery(key, value string) string {
	return deviceQuery(map[string]interface{}{
		"$and": []map[string]interface{}{
			{"tagValue": map[string]interface{}{"key": key, "value": value}},
		},
	})
}

// countBlock creates a device-count block.
func countBlock(id, title, appID string, p map[string]interface{}, query string) map[string]interface{} {
	return mergeBlock(id, "device-count", title, appID, p, map[string]interface{}{
		"conditions": []interface{}{},
		"defaultCondition": map[string]interface{}{
			"value": "{{value-0.count}}", "label": title, "color": "#8db319",
		},
		"segments": []map[string]interface{}{
			{"query": query, "id": "value-0"},
		},
	})
}

// sumGaugeBlock shows a sum of an attribute across devices with no upper bound display.
func sumGaugeBlock(id, title, appID string, p map[string]interface{}, query, attribute string) map[string]interface{} {
	return mergeBlock(id, "gauge", title, appID, p, map[string]interface{}{
		"gaugeType":           "dial",
		"realTime":            false,
		"precision":           "0",
		"precisionType":       "significant",
		"displayAsPercentage": false,
		"gaugeMin":            "0",
		"gaugeMax":            "1000",
		"duration":            0,
		"segment": map[string]interface{}{
			"query":     query,
			"attribute": attribute,
			"aggregation": map[string]interface{}{
				"type":    "SUM",
				"options": map[string]interface{}{},
			},
			"label": title,
			"color": "#e74c3c",
		},
		"conditions": []map[string]interface{}{},
	})
}

// graphBlock creates a time-series graph block.
func graphBlock(id, title, appID string, p map[string]interface{}, duration int, segments []map[string]interface{}) map[string]interface{} {
	return mergeBlock(id, "graph", title, appID, p, map[string]interface{}{
		"duration": duration,
		"segments": segments,
	})
}

// graphSegment builds one series for a graph block.
func graphSegment(query, attribute, label, color, aggType string) map[string]interface{} {
	return map[string]interface{}{
		"query":     query,
		"attribute": attribute,
		"aggregation": map[string]interface{}{
			"type":    aggType,
			"options": map[string]interface{}{},
		},
		"label": label,
		"color": color,
	}
}

// indicatorBlock creates an indicator block with color-coded conditions.
// Conditions are evaluated in order; the first match wins.
// Note: the indicator's data source (which device/attribute to read) must be
// configured in the Losant UI after creation — the API does not accept a
// segment/deviceId field alongside conditions.
func indicatorBlock(id, title, appID string, p map[string]interface{}, conditions []map[string]interface{}, defaultColor, defaultLabel string) map[string]interface{} {
	return mergeBlock(id, "indicator", title, appID, p, map[string]interface{}{
		"conditions": conditions,
		"defaultCondition": map[string]interface{}{
			"color": defaultColor,
			"label": defaultLabel,
		},
	})
}

// placeholderBlock creates a block with empty segments for types that require
// UI-level configuration (pie, custom-chart, custom-html).
func placeholderBlock(id, blockType, title, appID string, p map[string]interface{}) map[string]interface{} {
	return mergeBlock(id, blockType, title, appID, p, map[string]interface{}{
		"segments": []interface{}{},
	})
}

// ---- Dashboard 2: Cluster Detail (created first) ----------------------------

func clusterDetailDashboard(appID, rancherWorkflowID, rancherConnectKey, rancherDisconnectKey string) map[string]interface{} {
	q := edgeQuery()
	var y int

	// 2A: custom-html placeholder for header (cluster name + region)
	block2A := placeholderBlock("2a", "custom-html", "Cluster Info", appID, pos(0, y, 4, 1))
	y++

	// 2B: Gauge (Dial) — health_score 0-100
	block2B := mergeBlock("2b", "gauge", "Health Score", appID, pos(0, y, 1, 2), map[string]interface{}{
		"gaugeType":           "dial",
		"realTime":            false,
		"precision":           "0",
		"precisionType":       "significant",
		"displayAsPercentage": false,
		"gaugeMin":            "0",
		"gaugeMax":            "100",
		"duration":            0,
		"segment": map[string]interface{}{
			"query":     q,
			"attribute": "health_score",
			"aggregation": map[string]interface{}{
				"type":    "MEAN",
				"options": map[string]interface{}{},
			},
			"label": "Health Score",
			"color": "#8db319",
		},
		"conditions": []map[string]interface{}{
			{"condition": "({{gauge.range}} * (2/3)) + {{gauge.min}}", "color": "#e74c3c", "id": "c1"},
			{"condition": "({{gauge.range}} * (5/6)) + {{gauge.min}}", "color": "#f39c12", "id": "c2"},
			{"condition": "{{gauge.max}}", "color": "#27ae60", "id": "c3"},
		},
	})

	// 2C: Indicator — health_status
	block2C := indicatorBlock("2c", "Health Status", appID, pos(1, y, 1, 1),
		[]map[string]interface{}{
			{"condition": "{{value}} === 'healthy'", "color": "#27ae60", "label": "Healthy", "id": "i1"},
			{"condition": "{{value}} === 'degraded'", "color": "#f39c12", "label": "Degraded", "id": "i2"},
			{"condition": "{{value}} === 'critical'", "color": "#e74c3c", "label": "Critical", "id": "i3"},
		}, "#95a5a6", "Unknown")

	// 2D: Indicator — rancher_session_phase
	block2D := indicatorBlock("2d", "Rancher Session", appID, pos(1, y+1, 1, 1),
		[]map[string]interface{}{
			{"condition": "{{value}} === 'Connected'", "color": "#27ae60", "label": "Connected", "id": "r1"},
			{"condition": "{{value}} === 'Connecting'", "color": "#3498db", "label": "Connecting", "id": "r2"},
			{"condition": "{{value}} === 'Disconnecting'", "color": "#f39c12", "label": "Disconnecting", "id": "r3"},
			{"condition": "{{value}} === 'Failed'", "color": "#e74c3c", "label": "Failed", "id": "r4"},
		}, "#95a5a6", "None")

	// 2E1-2E4: Pod count gauges (right side, rows y and y+1)
	block2E1 := sumGaugeBlock("2e1", "Total Pods", appID, pos(2, y, 1, 1), q, "total_pods")
	block2E2 := sumGaugeBlock("2e2", "Running Pods", appID, pos(3, y, 1, 1), q, "running_pods")
	block2E3 := sumGaugeBlock("2e3", "Failed Pods", appID, pos(2, y+1, 1, 1), q, "failed_pods")
	block2E4 := sumGaugeBlock("2e4", "CrashLoop Pods", appID, pos(3, y+1, 1, 1), q, "crashloop_pods")
	y += 2

	// 2F: Infrastructure gauges
	block2F1 := sumGaugeBlock("2f1", "Total Nodes", appID, pos(0, y, 1, 1), q, "total_nodes")
	block2F2 := sumGaugeBlock("2f2", "Ready Nodes", appID, pos(1, y, 1, 1), q, "ready_nodes")
	block2F3 := sumGaugeBlock("2f3", "Unhealthy Nodes", appID, pos(2, y, 1, 1), q, "unhealthy_nodes")
	block2F4 := sumGaugeBlock("2f4", "Degraded PVCs", appID, pos(3, y, 1, 1), q, "degraded_pvcs")
	y++

	// 2G: Time series — health_score
	block2G := graphBlock("2g", "Health Score (24h)", appID, pos(0, y, 4, 2), 86400,
		[]map[string]interface{}{graphSegment(q, "health_score", "Health Score", "#8db319", "MEAN")})
	y += 2

	// 2H: Time series — pod counts
	block2H := graphBlock("2h", "Pod Counts (24h)", appID, pos(0, y, 4, 2), 86400,
		[]map[string]interface{}{
			graphSegment(q, "running_pods", "Running", "#27ae60", "LAST"),
			graphSegment(q, "failed_pods", "Failed", "#e74c3c", "LAST"),
			graphSegment(q, "pending_pods", "Pending", "#f39c12", "LAST"),
			graphSegment(q, "crashloop_pods", "CrashLoop", "#8e44ad", "LAST"),
		})
	y += 2

	// 2I: Time series — event_warnings
	block2I := graphBlock("2i", "Event Warnings (24h)", appID, pos(0, y, 4, 2), 86400,
		[]map[string]interface{}{graphSegment(q, "event_warnings", "Warnings", "#e74c3c", "LAST")})
	y += 2

	// 2J: Device state table — all peripheral devices
	block2J := mergeBlock("2j", "device-state-table", "Node States", appID, pos(0, y, 4, 4),
		map[string]interface{}{
			"duration":   0,
			"query":      deviceQuery(map[string]interface{}{"$and": []map[string]interface{}{{"deviceClass": map[string]interface{}{"$eq": "peripheral"}}}}),
			"attributes": []string{"health_score", "health_status", "ready", "pod_count", "cpu_request_pct", "mem_request_pct"},
			"columns": []map[string]interface{}{
				{"type": "deviceName", "headerTemplate": "Node", "rowTemplate": "{{format value}}", "id": "col-name"},
				{"type": "timestamp", "headerTemplate": "Last Report", "rowTemplate": "{{format value}}", "id": "col-ts"},
				{"type": "attribute", "headerTemplate": "Health", "rowTemplate": "{{format value}}", "id": "col-health", "attribute": "health_status"},
				{"type": "attribute", "headerTemplate": "Score", "rowTemplate": "{{format value}}", "id": "col-score", "attribute": "health_score"},
				{"type": "attribute", "headerTemplate": "Pods", "rowTemplate": "{{format value}}", "id": "col-pods", "attribute": "pod_count"},
				{"type": "attribute", "headerTemplate": "CPU%", "rowTemplate": "{{format value}}", "id": "col-cpu", "attribute": "cpu_request_pct"},
				{"type": "attribute", "headerTemplate": "Mem%", "rowTemplate": "{{format value}}", "id": "col-mem", "attribute": "mem_request_pct"},
			},
		})
	y += 4

	// 2K: Time series for CPU and memory per cluster
	block2K := graphBlock("2k", "Resource Utilization (24h)", appID, pos(0, y, 4, 2), 86400,
		[]map[string]interface{}{
			graphSegment(q, "cpu_request_pct", "CPU %", "#3498db", "MEAN"),
			graphSegment(q, "mem_request_pct", "Memory %", "#9b59b6", "MEAN"),
		})
	y += 2

	blocks := []map[string]interface{}{
		block2A, block2B, block2C, block2D,
		block2E1, block2E2, block2E3, block2E4,
		block2F1, block2F2, block2F3, block2F4,
		block2G, block2H, block2I, block2J, block2K,
	}

	// 2L: Input controls — Rancher connect/disconnect
	if rancherWorkflowID != "" && rancherConnectKey != "" && rancherDisconnectKey != "" {
		block2L := mergeBlock("2l", "input", "Rancher Session Controls", appID, pos(0, y, 4, 2),
			map[string]interface{}{
				"defaultMode": "unlocked",
				"controls": []map[string]interface{}{
					{
						"action":     "workflow",
						"color":      "#27ae60",
						"grid":       map[string]interface{}{"x": 0, "y": 0, "w": 2, "h": 1},
						"label":      "Connect to Rancher",
						"type":       "button",
						"id":         "button-0",
						"templateId": "button-0",
						"workflowId": rancherWorkflowID,
						"buttonId":   rancherConnectKey,
						"payload":    fmt.Sprintf(`{"action":"connect","nodeKey":%q}`, rancherConnectKey),
					},
					{
						"action":     "workflow",
						"color":      "#e74c3c",
						"grid":       map[string]interface{}{"x": 2, "y": 0, "w": 2, "h": 1},
						"label":      "Disconnect from Rancher",
						"type":       "button",
						"id":         "button-1",
						"templateId": "button-1",
						"workflowId": rancherWorkflowID,
						"buttonId":   rancherDisconnectKey,
						"payload":    fmt.Sprintf(`{"action":"disconnect","nodeKey":%q}`, rancherDisconnectKey),
					},
				},
			})
		blocks = append(blocks, block2L)
	}

	return map[string]interface{}{
		"name":   nameClusterDetail,
		"public": false,
		"blocks": blocks,
	}
}

// ---- Dashboard 1: Fleet Overview --------------------------------------------

func fleetOverviewDashboard(appID, clusterDetailDashboardID string) map[string]interface{} {
	q := edgeQuery()
	var y int

	// 1A: 4 device-count blocks — Total / Healthy / Degraded / Critical
	block1A1 := countBlock("1a1", "Total Clusters", appID, pos(0, y, 1, 1), q)
	block1A2 := countBlock("1a2", "Healthy (≥80)", appID, pos(1, y, 1, 1), tagQuery("health_status", "healthy"))
	block1A3 := countBlock("1a3", "Degraded (50-79)", appID, pos(2, y, 1, 1), tagQuery("health_status", "degraded"))
	block1A4 := countBlock("1a4", "Critical (<50)", appID, pos(3, y, 1, 1), tagQuery("health_status", "critical"))
	y++

	// 1B: Pie — health distribution (placeholder; configure query in UI)
	block1B := placeholderBlock("1b", "pie", "Health Distribution", appID, pos(0, y, 2, 2))

	// 1C: Aggregate problem pod/pvc counts
	block1C1 := sumGaugeBlock("1c1", "Failed Pods (Fleet)", appID, pos(2, y, 1, 1), q, "failed_pods")
	block1C2 := sumGaugeBlock("1c2", "CrashLoop Pods (Fleet)", appID, pos(3, y, 1, 1), q, "crashloop_pods")
	block1C3 := sumGaugeBlock("1c3", "Degraded PVCs (Fleet)", appID, pos(2, y+1, 1, 1), q, "degraded_pvcs")
	block1C4 := sumGaugeBlock("1c4", "Unhealthy Nodes (Fleet)", appID, pos(3, y+1, 1, 1), q, "unhealthy_nodes")
	y += 2

	// 1D: GPS History map — all edgeCompute devices colored by health
	popupURL := fmt.Sprintf("https://app.losant.com/dashboards/%s", clusterDetailDashboardID)
	block1D := mergeBlock("1d", "map", "Cluster Map", appID, pos(0, y, 4, 4),
		map[string]interface{}{
			"pinMode":              "advanced",
			"defaultZoom":          "auto",
			"includeLines":         false,
			"includeArrows":        false,
			"defaultCenter":        "",
			"defaultPitch":         0,
			"defaultBearing":       0,
			"centerOnDataPoints":   true,
			"clusterPoints":        true,
			"compositeResult":      false,
			"resizedPins":          true,
			"locationTagKey":       "gps",
			"startColor":           "#e74c3c",
			"endColor":             "#27ae60",
			"additionalAttributes": []string{"health_score", "health_status"},
			"query":                q,
			"popupTemplate": fmt.Sprintf(
				"##### **{{deviceName}}**\n##### Health Score: {{data.health_score}}\n##### Status: {{data.health_status}}\n[View Cluster Detail](%s)",
				popupURL),
			"iconTemplate": "{{#lt data.health_score 50}}\n  {{colorMarker '#e74c3c'}}\n{{else}}\n  {{#lt data.health_score 80}}\n    {{colorMarker '#f39c12'}}\n  {{else}}\n    {{colorMarker '#27ae60'}}\n  {{/lt}}\n{{/lt}}",
		})
	y += 4

	// 1H: Time Series — avg health_score all clusters 24h
	block1H := graphBlock("1h", "Avg Health Score — All Clusters (24h)", appID, pos(0, y, 4, 2), 86400,
		[]map[string]interface{}{graphSegment(q, "health_score", "Avg Health Score", "#8db319", "MEAN")})
	y += 2

	// 1E: Custom Chart — Treemap (Vega-Lite placeholder)
	block1E := placeholderBlock("1e", "custom-chart", "Cluster Health Treemap (configure in UI)", appID, pos(0, y, 4, 3))
	// Store the vega spec in the title since config can't hold it via API
	block1E["description"] = specTreemap
	y += 3

	// 1F: Custom Chart — Strip plot (Vega-Lite placeholder)
	block1F := placeholderBlock("1f", "custom-chart", "Health by Region (configure in UI)", appID, pos(0, y, 2, 2))
	block1F["description"] = specStripPlotRegion

	// 1G: Custom Chart — Heatmap (Vega-Lite placeholder)
	block1G := placeholderBlock("1g", "custom-chart", "Metric Heatmap (configure in UI)", appID, pos(2, y, 2, 2))
	block1G["description"] = specHeatmap
	y += 2

	// 1I: Custom HTML — Region tiles (placeholder)
	block1I := placeholderBlock("1i", "custom-html", "Region Health Tiles (configure in UI)", appID, pos(0, y, 4, 2))
	block1I["description"] = htmlRegionTiles
	y += 2

	// 1J-1: Count of clusters not reporting (compare last_sync_epoch)
	block1J1 := countBlock("1j1", "Total Clusters (reporting)", appID, pos(0, y, 1, 1), q)

	// 1J-2: Staleness strip plot (placeholder)
	block1J2 := placeholderBlock("1j2", "custom-chart", "Staleness by Region (configure in UI)", appID, pos(1, y, 3, 2))
	block1J2["description"] = specStripStaleness
	y++

	// 1J-3: Active reporting clusters time series (last 6h)
	block1J3 := graphBlock("1j3", "Active Reporting Clusters (6h)", appID, pos(0, y+1, 4, 2), 21600,
		[]map[string]interface{}{graphSegment(q, "health_score", "Active Clusters", "#8db319", "COUNT")})

	blocks := []map[string]interface{}{
		block1A1, block1A2, block1A3, block1A4,
		block1B, block1C1, block1C2, block1C3, block1C4,
		block1D, block1H,
		block1E, block1F, block1G,
		block1I,
		block1J1, block1J2, block1J3,
	}

	return map[string]interface{}{
		"name":   nameFleetOverview,
		"public": false,
		"blocks": blocks,
	}
}

// ---- main -------------------------------------------------------------------

func main() {
	var (
		appID                string
		apiToken             string
		force                bool
		rancherWFID          string
		rancherConnectKey    string
		rancherDisconnectKey string
	)
	flag.StringVar(&appID, "app-id", os.Getenv("LOSANT_APP_ID"), "Losant Application ID (or LOSANT_APP_ID env)")
	flag.StringVar(&apiToken, "api-token", os.Getenv("LOSANT_API_TOKEN"), "Losant Application API Token (or LOSANT_API_TOKEN env)")
	flag.BoolVar(&force, "force", false, "Delete and recreate dashboards if they already exist")
	flag.StringVar(&rancherWFID, "rancher-workflow-id", "", "Losant Workflow ID for Rancher connect/disconnect virtual buttons")
	flag.StringVar(&rancherConnectKey, "rancher-connect-key", os.Getenv("LOSANT_RANCHER_CONNECT_KEY"), "Virtual button node key for the Rancher connect action (or LOSANT_RANCHER_CONNECT_KEY env)")
	flag.StringVar(&rancherDisconnectKey, "rancher-disconnect-key", os.Getenv("LOSANT_RANCHER_DISCONNECT_KEY"), "Virtual button node key for the Rancher disconnect action (or LOSANT_RANCHER_DISCONNECT_KEY env)")
	flag.Parse()

	if appID == "" {
		log.Fatal("--app-id or LOSANT_APP_ID is required")
	}
	if apiToken == "" {
		log.Fatal("--api-token or LOSANT_API_TOKEN is required")
	}

	c := &apiClient{
		appID: appID,
		token: apiToken,
		http:  &http.Client{Timeout: 30 * time.Second},
	}

	fmt.Println("Creating Cluster Detail dashboard (step 1 of 2)...")
	clusterDetailID, err := c.ensureDashboard(nameClusterDetail, clusterDetailDashboard(appID, rancherWFID, rancherConnectKey, rancherDisconnectKey), force)
	if err != nil {
		log.Fatalf("cluster detail: %v", err)
	}

	fmt.Println("Creating Fleet Overview dashboard (step 2 of 2)...")
	fleetOverviewID, err := c.ensureDashboard(nameFleetOverview, fleetOverviewDashboard(appID, clusterDetailID), force)
	if err != nil {
		log.Fatalf("fleet overview: %v", err)
	}

	fmt.Println()
	fmt.Println("=== Done ===")
	fmt.Printf("Fleet Overview:  %s/%s\n", dashboardUIBase, fleetOverviewID)
	fmt.Printf("Cluster Detail:  %s/%s\n", dashboardUIBase, clusterDetailID)
	fmt.Println()
	fmt.Println("Post-creation steps:")
	fmt.Println("  1. In Cluster Detail, add a context variable (Dashboard Settings → Context Variables):")
	fmt.Println("     Name: deviceId  Type: Device ID  Filter: deviceClass=edgeCompute")
	fmt.Println("  2. Blocks marked '(configure in UI)' are placeholders — set their Vega-Lite")
	fmt.Println("     specs or HTML in the block editor. Specs are stored in the block description.")
	fmt.Println("  3. Indicator blocks (Health Status, Rancher Session) need their data source")
	fmt.Println("     configured in the block editor (Device + Attribute fields).")

	if rancherWFID == "" || rancherConnectKey == "" || rancherDisconnectKey == "" {
		var missing []string
		if rancherWFID == "" {
			missing = append(missing, "--rancher-workflow-id")
		}
		if rancherConnectKey == "" {
			missing = append(missing, "--rancher-connect-key")
		}
		if rancherDisconnectKey == "" {
			missing = append(missing, "--rancher-disconnect-key")
		}
		fmt.Println()
		fmt.Printf("Note: Block 2L (Rancher buttons) was omitted — missing: %s\n", strings.Join(missing, ", "))
		fmt.Println("Re-run with --force and all three flags to add it.")
	}
}
