// create-dashboards.go scaffolds the Fleet Overview and Cluster Detail Losant
// dashboards for a k8s-cluster operator deployment. Run with:
//
//	go run scripts/create-dashboards.go --app-id $LOSANT_APP_ID --api-token $LOSANT_API_TOKEN
//
// The script is idempotent: it skips creation when a dashboard with the same
// canonical name already exists (unless --force is set).
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
// Losant Data API call is injected by the custom-html block config.
// The dashboard context provides window.losantData with device state data.
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

// bounds returns a Losant grid bounds object. x and y are 0-based; w and h are column/row counts.
func bounds(x, y, w, h int) map[string]int {
	return map[string]int{"x": x, "y": y, "w": w, "h": h}
}

// ---- Dashboard 2: Cluster Detail (created first) ----------------------------

func clusterDetailDashboard(appID, rancherWorkflowID string) map[string]interface{} {
	// Context variable: deviceId (device ID picker)
	ctxVars := []map[string]interface{}{{
		"id":            "deviceId",
		"defaultValue":  "",
		"label":         "Cluster",
		"type":          "deviceId",
		"applicationId": appID,
		"deviceTags":    []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
	}}

	healthStatusStates := []map[string]interface{}{
		{"condition": "{{value}} === 'healthy'", "label": "Healthy", "color": "#27ae60"},
		{"condition": "{{value}} === 'degraded'", "label": "Degraded", "color": "#f39c12"},
		{"condition": "{{value}} === 'critical'", "label": "Critical", "color": "#e74c3c"},
	}
	rancherPhaseStates := []map[string]interface{}{
		{"condition": "{{value}} === 'Connected'", "label": "Connected", "color": "#27ae60"},
		{"condition": "{{value}} === 'Connecting'", "label": "Connecting", "color": "#3498db"},
		{"condition": "{{value}} === 'Disconnecting'", "label": "Disconnecting", "color": "#f39c12"},
		{"condition": "{{value}} === 'Failed'", "label": "Failed", "color": "#e74c3c"},
		{"condition": "{{value}} === 'None'", "label": "None", "color": "#95a5a6"},
	}

	var y int

	// 2A: Text header (cluster name + region from device tags)
	block2A := map[string]interface{}{
		"id":        "2a",
		"blockType": "text",
		"title":     "",
		"bounds":    bounds(0, y, 12, 2),
		"config": map[string]interface{}{
			"text":           "## {{deviceName}}\n**Region:** {{deviceTags.region}}",
			"deviceId":       "{{ctx.deviceId}}",
			"renderMarkdown": true,
		},
	}
	y += 2

	// 2B: Dial gauge — health_score (0-100, color bands)
	block2B := map[string]interface{}{
		"id":        "2b",
		"blockType": "gauge",
		"title":     "Health Score",
		"bounds":    bounds(0, y, 4, 4),
		"config": map[string]interface{}{
			"duration":  0,
			"min":       0,
			"max":       100,
			"deviceId":  "{{ctx.deviceId}}",
			"attribute": "health_score",
			"gaugeMax":  100,
			"gaugeMin":  0,
			"segments": []map[string]interface{}{
				{"label": "Critical", "min": 0, "max": 50, "color": "#e74c3c"},
				{"label": "Degraded", "min": 50, "max": 80, "color": "#f39c12"},
				{"label": "Healthy", "min": 80, "max": 100, "color": "#27ae60"},
			},
		},
	}

	// 2C: Indicator — health_status
	block2C := map[string]interface{}{
		"id":        "2c",
		"blockType": "indicator",
		"title":     "Health Status",
		"bounds":    bounds(4, y, 4, 2),
		"config": map[string]interface{}{
			"duration":  0,
			"deviceId":  "{{ctx.deviceId}}",
			"attribute": "health_status",
			"states":    healthStatusStates,
		},
	}

	// 2D: Indicator — rancher_session_phase
	block2D := map[string]interface{}{
		"id":        "2d",
		"blockType": "indicator",
		"title":     "Rancher Session",
		"bounds":    bounds(4, y+2, 4, 2),
		"config": map[string]interface{}{
			"duration":  0,
			"deviceId":  "{{ctx.deviceId}}",
			"attribute": "rancher_session_phase",
			"states":    rancherPhaseStates,
		},
	}

	// 2E: 5 number gauges for pod counts (2 cols each, right side)
	podAttrs := []struct{ id, title, attr string }{
		{"2e1", "Total Pods", "total_pods"},
		{"2e2", "Running Pods", "running_pods"},
		{"2e3", "Failed Pods", "failed_pods"},
		{"2e4", "Pending Pods", "pending_pods"},
		{"2e5", "CrashLoop Pods", "crashloop_pods"},
	}
	var podBlocks []map[string]interface{}
	for i, p := range podAttrs {
		podBlocks = append(podBlocks, map[string]interface{}{
			"id":        p.id,
			"blockType": "number-gauge",
			"title":     p.title,
			"bounds":    bounds(8+(i%2)*2, y+(i/2)*2, 2, 2),
			"config": map[string]interface{}{
				"duration":    0,
				"displayText": "{{value}}",
				"deviceId":    "{{ctx.deviceId}}",
				"attribute":   p.attr,
			},
		})
	}
	y += 4

	// 2F: Node counts and infrastructure indicators
	infraAttrs := []struct{ id, title, attr string }{
		{"2f1", "Ready Nodes", "ready_nodes"},
		{"2f2", "Total Nodes", "total_nodes"},
		{"2f3", "Unhealthy Nodes", "unhealthy_nodes"},
		{"2f4", "Degraded PVCs", "degraded_pvcs"},
		{"2f5", "Event Warnings", "event_warnings"},
		{"2f6", "Last Sync (epoch)", "last_sync_epoch"},
	}
	var infraBlocks []map[string]interface{}
	for i, a := range infraAttrs {
		infraBlocks = append(infraBlocks, map[string]interface{}{
			"id":        a.id,
			"blockType": "number-gauge",
			"title":     a.title,
			"bounds":    bounds((i%6)*2, y, 2, 2),
			"config": map[string]interface{}{
				"duration":    0,
				"displayText": "{{value}}",
				"deviceId":    "{{ctx.deviceId}}",
				"attribute":   a.attr,
			},
		})
	}
	y += 2

	// 2G: Time series — health_score
	block2G := map[string]interface{}{
		"id":        "2g",
		"blockType": "time-series-graph",
		"title":     "Health Score (24h)",
		"bounds":    bounds(0, y, 12, 4),
		"config": map[string]interface{}{
			"duration":   86400,
			"resolution": map[string]interface{}{"type": "auto"},
			"queries": []map[string]interface{}{{
				"deviceId": "{{ctx.deviceId}}",
				"attributes": []map[string]interface{}{
					{"attribute": "health_score", "label": "Health Score", "aggregation": "MEAN"},
				},
				"yAxisLabel": "Score",
			}},
			"annotations": []map[string]interface{}{
				{"value": 80, "label": "Healthy threshold", "color": "#27ae60"},
				{"value": 50, "label": "Degraded threshold", "color": "#f39c12"},
			},
		},
	}
	y += 4

	// 2H: Time series — pod counts
	block2H := map[string]interface{}{
		"id":        "2h",
		"blockType": "time-series-graph",
		"title":     "Pod Counts (24h)",
		"bounds":    bounds(0, y, 12, 4),
		"config": map[string]interface{}{
			"duration":   86400,
			"resolution": map[string]interface{}{"type": "auto"},
			"queries": []map[string]interface{}{{
				"deviceId": "{{ctx.deviceId}}",
				"attributes": []map[string]interface{}{
					{"attribute": "running_pods", "label": "Running", "aggregation": "LAST"},
					{"attribute": "failed_pods", "label": "Failed", "aggregation": "LAST"},
					{"attribute": "pending_pods", "label": "Pending", "aggregation": "LAST"},
					{"attribute": "crashloop_pods", "label": "CrashLoop", "aggregation": "LAST"},
				},
			}},
		},
	}
	y += 4

	// 2I: Time series — event_warnings
	block2I := map[string]interface{}{
		"id":        "2i",
		"blockType": "time-series-graph",
		"title":     "Event Warnings (24h)",
		"bounds":    bounds(0, y, 12, 4),
		"config": map[string]interface{}{
			"duration":   86400,
			"resolution": map[string]interface{}{"type": "auto"},
			"queries": []map[string]interface{}{{
				"deviceId": "{{ctx.deviceId}}",
				"attributes": []map[string]interface{}{
					{"attribute": "event_warnings", "label": "Warnings", "aggregation": "LAST"},
				},
			}},
		},
	}
	y += 4

	// 2J: Device state table — peripheral devices for this cluster
	block2J := map[string]interface{}{
		"id":        "2j",
		"blockType": "device-state-table",
		"title":     "Node States",
		"bounds":    bounds(0, y, 12, 6),
		"config": map[string]interface{}{
			"deviceTags": []map[string]string{{"key": "clusterName", "value": "{{deviceTags.clusterName}}"}},
			"attributes": []string{"health_score", "health_status", "ready", "pod_count", "cpu_request_pct", "mem_request_pct"},
		},
	}
	y += 6

	// 2K: Time series for CPU and memory request % per node
	block2K := map[string]interface{}{
		"id":        "2k",
		"blockType": "time-series-graph",
		"title":     "Node Resource Utilization (24h)",
		"bounds":    bounds(0, y, 12, 4),
		"config": map[string]interface{}{
			"duration":   86400,
			"resolution": map[string]interface{}{"type": "auto"},
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "clusterName", "value": "{{deviceTags.clusterName}}"}},
				"attributes": []map[string]interface{}{
					{"attribute": "cpu_request_pct", "label": "CPU %", "aggregation": "MEAN"},
					{"attribute": "mem_request_pct", "label": "Memory %", "aggregation": "MEAN"},
				},
			}},
		},
	}
	y += 4

	// 2L: Input controls — Rancher connect/disconnect buttons
	rancherButtons := map[string]interface{}{
		"id":        "2l",
		"blockType": "input-controls",
		"title":     "Rancher Session Controls",
		"bounds":    bounds(0, y, 12, 2),
		"config": map[string]interface{}{
			"controls": []map[string]interface{}{
				{
					"type":       "button",
					"label":      "Connect to Rancher",
					"color":      "#27ae60",
					"workflowId": rancherWorkflowID,
					"virtualButton": map[string]interface{}{
						"key":     "rancher-connect",
						"payload": map[string]interface{}{"action": "connect", "deviceId": "{{ctx.deviceId}}"},
					},
				},
				{
					"type":       "button",
					"label":      "Disconnect from Rancher",
					"color":      "#e74c3c",
					"workflowId": rancherWorkflowID,
					"virtualButton": map[string]interface{}{
						"key":     "rancher-disconnect",
						"payload": map[string]interface{}{"action": "disconnect", "deviceId": "{{ctx.deviceId}}"},
					},
				},
			},
		},
	}

	var blocks []map[string]interface{}
	blocks = append(blocks, block2A, block2B, block2C, block2D)
	blocks = append(blocks, podBlocks...)
	blocks = append(blocks, infraBlocks...)
	blocks = append(blocks, block2G, block2H, block2I, block2J, block2K)
	if rancherWorkflowID != "" {
		blocks = append(blocks, rancherButtons)
	}

	return map[string]interface{}{
		"name":             nameClusterDetail,
		"public":           false,
		"contextVariables": ctxVars,
		"blocks":           blocks,
	}
}

// ---- Dashboard 1: Fleet Overview --------------------------------------------

func fleetOverviewDashboard(appID, clusterDetailDashboardID string) map[string]interface{} {
	var y int

	// 1A: 4 count gauges — Total / Healthy / Degraded / Critical clusters
	block1A1 := map[string]interface{}{
		"id":        "1a1",
		"blockType": "number-gauge",
		"title":     "Total Clusters",
		"bounds":    bounds(0, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{{"attribute": "health_score", "aggregation": "COUNT"}},
			},
		},
	}
	block1A2 := map[string]interface{}{
		"id":        "1a2",
		"blockType": "number-gauge",
		"title":     "Healthy (≥80)",
		"bounds":    bounds(3, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "health_status", "value": "healthy"}},
				"attributes": []map[string]interface{}{{"attribute": "health_score", "aggregation": "COUNT"}},
			},
		},
	}
	block1A3 := map[string]interface{}{
		"id":        "1a3",
		"blockType": "number-gauge",
		"title":     "Degraded (50-79)",
		"bounds":    bounds(6, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "health_status", "value": "degraded"}},
				"attributes": []map[string]interface{}{{"attribute": "health_score", "aggregation": "COUNT"}},
			},
		},
	}
	block1A4 := map[string]interface{}{
		"id":        "1a4",
		"blockType": "number-gauge",
		"title":     "Critical (<50)",
		"bounds":    bounds(9, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "health_status", "value": "critical"}},
				"attributes": []map[string]interface{}{{"attribute": "health_score", "aggregation": "COUNT"}},
			},
		},
	}
	y += 2

	// 1B: Pie chart — group by health_status
	block1B := map[string]interface{}{
		"id":        "1b",
		"blockType": "pie-chart",
		"title":     "Cluster Health Distribution",
		"bounds":    bounds(0, y, 4, 5),
		"config": map[string]interface{}{
			"duration": 0,
			"queries": []map[string]interface{}{{
				"label":       "Healthy",
				"color":       "#27ae60",
				"deviceTags":  []map[string]string{{"key": "health_status", "value": "healthy"}},
				"attribute":   "health_score",
				"aggregation": "COUNT",
			}, {
				"label":       "Degraded",
				"color":       "#f39c12",
				"deviceTags":  []map[string]string{{"key": "health_status", "value": "degraded"}},
				"attribute":   "health_score",
				"aggregation": "COUNT",
			}, {
				"label":       "Critical",
				"color":       "#e74c3c",
				"deviceTags":  []map[string]string{{"key": "health_status", "value": "critical"}},
				"attribute":   "health_score",
				"aggregation": "COUNT",
			}},
		},
	}

	// 1C: 3 number gauges — sum of failed_pods, crashloop_pods, degraded_pvcs
	block1C1 := map[string]interface{}{
		"id":        "1c1",
		"blockType": "number-gauge",
		"title":     "Failed Pods (all clusters)",
		"bounds":    bounds(4, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{{"attribute": "failed_pods", "aggregation": "SUM"}},
			},
		},
	}
	block1C2 := map[string]interface{}{
		"id":        "1c2",
		"blockType": "number-gauge",
		"title":     "CrashLoop Pods (all clusters)",
		"bounds":    bounds(7, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{{"attribute": "crashloop_pods", "aggregation": "SUM"}},
			},
		},
	}
	block1C3 := map[string]interface{}{
		"id":        "1c3",
		"blockType": "number-gauge",
		"title":     "Degraded PVCs (all clusters)",
		"bounds":    bounds(4, y+2, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{{"attribute": "degraded_pvcs", "aggregation": "SUM"}},
			},
		},
	}
	y += 5

	// 1D: GPS History — all edgeCompute devices
	popupURL := fmt.Sprintf("https://app.losant.com/dashboards/%s?deviceId={{deviceId}}", clusterDetailDashboardID)
	block1D := map[string]interface{}{
		"id":        "1d",
		"blockType": "map",
		"title":     "Cluster Map",
		"bounds":    bounds(0, y, 8, 6),
		"config": map[string]interface{}{
			"deviceTags":     []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
			"gpsAttribute":   "gps",
			"colorAttribute": "health_status",
			"colorMap": map[string]string{
				"healthy":  "#27ae60",
				"degraded": "#f39c12",
				"critical": "#e74c3c",
			},
			"popup": map[string]interface{}{
				"template": fmt.Sprintf(`<a href="%s" target="_blank">View Cluster Detail</a>`, popupURL),
			},
		},
	}

	// 1H: Time Series — avg health_score all clusters, 24h
	block1H := map[string]interface{}{
		"id":        "1h",
		"blockType": "time-series-graph",
		"title":     "Avg Health Score — All Clusters (24h)",
		"bounds":    bounds(8, y, 4, 6),
		"config": map[string]interface{}{
			"duration":   86400,
			"resolution": map[string]interface{}{"type": "auto"},
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "health_score", "label": "Avg Health Score", "aggregation": "MEAN"},
				},
			}},
			"annotations": []map[string]interface{}{
				{"value": 80, "label": "Healthy", "color": "#27ae60"},
				{"value": 50, "label": "Degraded", "color": "#f39c12"},
			},
		},
	}
	y += 6

	// 1E: Custom Chart (Vega-Lite) — Treemap
	block1E := map[string]interface{}{
		"id":        "1e",
		"blockType": "custom-chart",
		"title":     "Cluster Health Treemap",
		"bounds":    bounds(0, y, 12, 6),
		"config": map[string]interface{}{
			"vegaSpec": specTreemap,
			"duration": 0,
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "total_nodes", "aggregation": "LAST"},
					{"attribute": "health_score", "aggregation": "LAST"},
				},
			}},
		},
	}
	y += 6

	// 1F: Custom Chart — Strip plot (health_score by region)
	block1F := map[string]interface{}{
		"id":        "1f",
		"blockType": "custom-chart",
		"title":     "Health Score by Region",
		"bounds":    bounds(0, y, 6, 5),
		"config": map[string]interface{}{
			"vegaSpec": specStripPlotRegion,
			"duration": 0,
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "health_score", "aggregation": "LAST"},
				},
			}},
		},
	}

	// 1G: Custom Chart — Matrix heatmap (region × health metrics)
	block1G := map[string]interface{}{
		"id":        "1g",
		"blockType": "custom-chart",
		"title":     "Health Metrics by Region",
		"bounds":    bounds(6, y, 6, 5),
		"config": map[string]interface{}{
			"vegaSpec": specHeatmap,
			"duration": 0,
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "health_score", "aggregation": "MEAN"},
					{"attribute": "failed_pods", "aggregation": "SUM"},
					{"attribute": "unhealthy_nodes", "aggregation": "SUM"},
				},
			}},
		},
	}
	y += 5

	// 1I: Custom HTML — Region tile array
	block1I := map[string]interface{}{
		"id":        "1i",
		"blockType": "custom-html",
		"title":     "Region Health Overview",
		"bounds":    bounds(0, y, 12, 4),
		"config": map[string]interface{}{
			"html":       htmlRegionTiles,
			"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
			"attributes": []string{"health_score"},
		},
	}
	y += 4

	// 1J-1: Gauge — count of silent clusters (not reporting in >10 min)
	block1J1 := map[string]interface{}{
		"id":        "1j1",
		"blockType": "number-gauge",
		"title":     "Silent Clusters (>10 min)",
		"bounds":    bounds(0, y, 3, 2),
		"config": map[string]interface{}{
			"duration": 0,
			"query": map[string]interface{}{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{{"attribute": "last_sync_epoch", "aggregation": "COUNT"}},
			},
			"filter": "{{value}} < {{subtractSeconds now 600}}",
		},
	}

	// 1J-2: Custom Chart — Strip plot: X=minutes since last report, Y=region
	block1J2 := map[string]interface{}{
		"id":        "1j2",
		"blockType": "custom-chart",
		"title":     "Time Since Last Report by Region",
		"bounds":    bounds(3, y, 5, 4),
		"config": map[string]interface{}{
			"vegaSpec": specStripStaleness,
			"duration": 0,
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "last_sync_epoch", "aggregation": "LAST"},
				},
			}},
		},
	}

	// 1J-3: Time Series — count of actively reporting clusters per 5-min window
	block1J3 := map[string]interface{}{
		"id":        "1j3",
		"blockType": "time-series-graph",
		"title":     "Active Reporting Clusters (6h)",
		"bounds":    bounds(8, y, 4, 4),
		"config": map[string]interface{}{
			"duration":   21600,
			"resolution": map[string]interface{}{"type": "time", "value": 300},
			"queries": []map[string]interface{}{{
				"deviceTags": []map[string]string{{"key": "deviceClass", "value": "edgeCompute"}},
				"attributes": []map[string]interface{}{
					{"attribute": "health_score", "label": "Active Clusters", "aggregation": "COUNT"},
				},
			}},
		},
	}

	blocks := []map[string]interface{}{
		block1A1, block1A2, block1A3, block1A4,
		block1B, block1C1, block1C2, block1C3,
		block1D, block1H,
		block1E,
		block1F, block1G,
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
		appID       string
		apiToken    string
		force       bool
		rancherWFID string
	)
	flag.StringVar(&appID, "app-id", os.Getenv("LOSANT_APP_ID"), "Losant Application ID (or LOSANT_APP_ID env)")
	flag.StringVar(&apiToken, "api-token", os.Getenv("LOSANT_API_TOKEN"), "Losant Application API Token (or LOSANT_API_TOKEN env)")
	flag.BoolVar(&force, "force", false, "Delete and recreate dashboards if they already exist")
	flag.StringVar(&rancherWFID, "rancher-workflow-id", "", "Losant Workflow ID for Rancher connect/disconnect virtual buttons")
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
	clusterDetailID, err := c.ensureDashboard(nameClusterDetail, clusterDetailDashboard(appID, rancherWFID), force)
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
	if rancherWFID == "" {
		fmt.Println()
		fmt.Println("Note: --rancher-workflow-id was not set; Block 2L (Rancher buttons) was omitted.")
		fmt.Println("Re-run with --force --rancher-workflow-id <id> to add it.")
	}
}
