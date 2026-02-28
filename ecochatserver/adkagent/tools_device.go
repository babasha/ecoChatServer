package adkagent

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ============================================================================
// DEVICE TOOLS — 6 tools for Zefir IoT sensor management
// API-based (2 tools) + Static guides (4 tools)
// ============================================================================

// ZefirUserIDProvider provides the current user's Zefir ID for API calls
type ZefirUserIDProvider interface {
	GetZefirUserID() string
}

// CreateDeviceTools creates 6 device-related tools
func CreateDeviceTools(zefirClient *ZefirClient, userIDProvider *ZefirUserIDProvider) ([]tool.Tool, error) {
	var tools []tool.Tool

	// Tool 1: get_user_devices — live API
	t1, err := createGetUserDevicesTool(zefirClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, t1)

	// Tool 2: get_sensor_reading — live API
	t2, err := createGetSensorReadingTool(zefirClient, userIDProvider)
	if err != nil {
		return nil, err
	}
	tools = append(tools, t2)

	// Tool 3: get_setup_guide — static
	t3, err := createGetSetupGuideTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t3)

	// Tool 4: troubleshoot_device — static
	t4, err := createTroubleshootDeviceTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t4)

	// Tool 5: get_mesh_info — static
	t5, err := createGetMeshInfoTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t5)

	// Tool 6: get_hardware_specs — static
	t6, err := createGetHardwareSpecsTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t6)

	log.Printf("[TOOLS_DEVICE] Created %d device tools", len(tools))
	return tools, nil
}

// ============================================================================
// API-BASED TOOLS (live data from Zefir backend)
// ============================================================================

func createGetUserDevicesTool(zefirClient *ZefirClient, userIDProvider *ZefirUserIDProvider) (tool.Tool, error) {
	type Input struct {
		Format string `json:"format,omitempty"` // optional: "short" or "detailed" (default: detailed)
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_user_devices",
			Description: "List user's sensor devices with status and battery.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_user_devices called")

			if userIDProvider == nil || *userIDProvider == nil {
				return Output{Result: "Error: agent not initialized"}, nil
			}

			userID := (*userIDProvider).GetZefirUserID()
			if userID == "" {
				return Output{Result: "No Zefir account linked. Please ensure your Zefir user ID is associated with this chat."}, nil
			}

			devices, err := zefirClient.GetUserDevices(ctx, userID)
			if err != nil {
				log.Printf("[TOOL] get_user_devices error: %v", err)
				return Output{Result: fmt.Sprintf("Error fetching devices: %v", err)}, nil
			}

			if len(devices) == 0 {
				return Output{Result: "You don't have any devices yet. Use get_setup_guide(step='overview') to learn how to set up your first Zefir sensor!"}, nil
			}

			result := FormatDevicesList(devices)
			return Output{Result: result}, nil
		},
	)
}

func createGetSensorReadingTool(zefirClient *ZefirClient, userIDProvider *ZefirUserIDProvider) (tool.Tool, error) {
	type Input struct {
		DeviceID string `json:"deviceID"` // Device ID to read
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_sensor_reading",
			Description: "Get latest sensor reading: humidity, temperature, battery. Requires deviceID.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_sensor_reading called: deviceID=%s", input.DeviceID)

			if input.DeviceID == "" {
				return Output{Result: "Error: deviceID is required. Use get_user_devices to find your device IDs."}, nil
			}

			reading, err := zefirClient.GetLatestReading(ctx, input.DeviceID)
			if err != nil {
				log.Printf("[TOOL] get_sensor_reading error: %v", err)
				return Output{Result: fmt.Sprintf("Error fetching reading for device %s: %v", input.DeviceID, err)}, nil
			}

			result := FormatSensorReading(reading)
			return Output{Result: result}, nil
		},
	)
}

// ============================================================================
// STATIC TOOLS (embedded guides)
// ============================================================================

func createGetSetupGuideTool() (tool.Tool, error) {
	type Input struct {
		Step string `json:"step"` // overview, unboxing, ble_pairing, wifi_config, mesh_setup, plant_assign, first_reading
	}
	type Output struct {
		Result string `json:"result"`
	}

	setupGuides := map[string]string{
		"overview": `Setup (~5 min): 1.Unbox 2.BLE pair 3.WiFi 4.Mesh(optional) 5.Assign plant 6.First reading
Needs: Zefir app, 2.4GHz WiFi, charged sensor.`,

		"unboxing": `Box: sensor + probe + USB-C cable + quick start card.
Before: check LED (green=ready, red=low battery, none=press button to wake). Download Zefir app.`,

		"ble_pairing": `1. App → "Add Device"
2. Hold sensor button 3s → BLUE LED blinks
3. Tap "Zefir-XXXX" in app → wait 5-10s
If not found: Bluetooth ON, keep <1m, press button again. iOS: check BLE permission. Android: enable Location. Last resort: hold 10s = factory reset.`,

		"wifi_config": `After BLE pairing:
1. Select 2.4GHz WiFi → enter password → Connect
2. GREEN LED = connected
Note: ESP32-C3 = 2.4GHz ONLY (no 5GHz). Hidden SSID: type manually. Dual-band router: disable band steering or create separate 2.4GHz SSID.`,

		"mesh_setup": `For 2+ sensors: ROOT elected by WiFi RSSI (strongest signal), others = NODE (relay via ESP-NOW, no WiFi needed).
Setup: add sensors via app, they auto-discover mesh. Up to 20 ESP-NOW peers. Re-election if ROOT offline 65s. Place ROOT near router, NODEs within 10m of any node. Self-healing topology.`,

		"plant_assign": `1. Tap sensor → "Assign Plant" → search 89 species or "Custom"
Auto-configures: moisture thresholds, temp range, watering reminders. Change anytime.`,

		"first_reading": `Insert probe 2/3 depth into soil, wait 30s.
Readings: Moisture 0-100%, Temp °C, Battery %, Signal dBm.
Default interval: 30 min (configurable 15min–6hr, more frequent = more drain).`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_setup_guide",
			Description: "Setup guide. Steps: overview/unboxing/ble_pairing/wifi_config/mesh_setup/plant_assign/first_reading.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_setup_guide called: step=%s", input.Step)

			step := strings.ToLower(input.Step)
			if step == "" {
				step = "overview"
			}

			guide, ok := setupGuides[step]
			if !ok {
				return Output{Result: fmt.Sprintf("Unknown step '%s'. Available: overview, unboxing, ble_pairing, wifi_config, mesh_setup, plant_assign, first_reading.", input.Step)}, nil
			}

			return Output{Result: guide}, nil
		},
	)
}

func createTroubleshootDeviceTool() (tool.Tool, error) {
	type Input struct {
		Issue string `json:"issue"` // not_connecting, no_readings, wrong_values, battery_drain, offline, led_error, wifi_lost, mesh_disconnect, app_crash, firmware_update
	}
	type Output struct {
		Result string `json:"result"`
	}

	troubleshootingGuides := map[string]string{
		"not_connecting": `Won't connect:
1. No LED? Charge 30min via USB-C
2. Hold button 3s → BLUE blink → app refresh → keep <1m
3. Still fails: restart app, toggle Bluetooth, check permissions (iOS: BLE, Android: Location)
4. Last resort: hold 10s = factory reset (re-setup needed)`,

		"no_readings": `No readings:
1. Check probe: firmly attached? No damage?
2. Probe in soil? Insert 2/3 depth, loosen compacted soil
3. Sensor "Online" in app? If offline: charge + restart
4. Default interval 30min — tap "Read Now" for immediate
5. Press button to wake, charge if needed`,

		"wrong_values": `Wrong values:
Moisture 0%: probe not in soil or extremely dry. 100%: in water/saturated.
Fix: remove probe, dry, reinsert, wait 5min to stabilize.
Soil types: sandy=lower, clay=higher. Recalibrate: Device Settings → Calibrate.
Temp: measures SOIL not air (normal difference). Wait 10min after inserting.
Battery wrong: restart sensor, charge fully.`,

		"battery_drain": `Battery life: NODE ~21 months (2000mAh, deep sleep ~10μA, active ~80mA). ROOT ~40 days (WiFi always on, 5-10mA).
• Increase interval (15min-6hr) in Settings
• Weak WiFi (RSSI < -70dBm): move closer or use mesh
• Mesh relay overhead: position ROOT centrally
• Cold (<5°C): normal lithium battery behavior
• Check firmware updates: Settings → Firmware`,

		"offline": `Sensor offline:
1. No LED? Battery dead — charge 30min
2. ROOT sensor: WiFi working? Password changed? → factory reset + re-setup
3. NODE sensor: ROOT online? Move closer or add intermediate node
4. Range: WiFi ~15m, mesh ~10m through walls
5. Server down? Sensor stores data locally, syncs later`,

		"led_error": `LED patterns (GPIO8):
• Boot: 3 fast blinks (100ms) — initializing
• Election: 2 slow blinks (300ms) — root election in progress
• Sync: double quick blink (50ms) — data sync
• ROOT active: solid ON / NODE active: solid OFF
• Error: 3 rapid blinks (80ms) — WiFi/probe/memory issue, restart
• Heartbeat: single short blink (30ms) — normal operation
• Charging: WHITE. Firmware update: YELLOW (don't unplug!)
BLUE=BLE mode, RED slow=low battery, RED solid=hardware fault (contact support)`,

		"wifi_lost": `WiFi drops:
1. RSSI < -80dBm? Move closer to router
2. Restart router, ensure 2.4GHz enabled, disable band steering
3. Password changed? Factory reset (hold 10s) + re-setup
4. Too many devices? Assign static IP in router
5. Unreliable location? Use mesh instead of direct WiFi`,

		"mesh_disconnect": `Mesh issues:
1. ROOT down? All NODEs lose connectivity — fix ROOT first
2. Range: ESP-NOW ~10m through walls. Move closer or add relay node
3. Interference: microwaves, metal walls. Relocate sensor
4. Check topology: Settings → Mesh Network
5. Re-mesh: Device Settings → Leave Mesh, move closer, re-add`,

		"app_crash": `App issues:
1. Force close + reopen
2. Clear cache: Android Settings→Apps→Zefir→Clear Cache, iOS reinstall, Desktop delete ~/.zefir/cache/
3. Update app from store or zefir.app
4. Login issue: log out/in, check internet, status: zefir.app/status`,

		"firmware_update": `Firmware update:
App → Device Settings → Firmware → Update. LED=YELLOW during update (1-3min). DON'T unplug!
Fails? Battery >30%, keep phone close, stable WiFi. Resumable.
Bricked? USB-C to PC → Zefir Flash Tool from zefir.app/tools`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "troubleshoot_device",
			Description: "Troubleshoot sensor issues: not_connecting/no_readings/wrong_values/battery_drain/offline/wifi_lost/mesh_disconnect.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] troubleshoot_device called: issue=%s", input.Issue)

			issue := strings.ToLower(input.Issue)
			if issue == "" {
				return Output{Result: "Error: issue type is required. Available: not_connecting, no_readings, wrong_values, battery_drain, offline, led_error, wifi_lost, mesh_disconnect, app_crash, firmware_update."}, nil
			}

			guide, ok := troubleshootingGuides[issue]
			if !ok {
				// Try fuzzy matching
				for key, val := range troubleshootingGuides {
					if strings.Contains(issue, key) || strings.Contains(key, issue) {
						return Output{Result: val}, nil
					}
				}
				return Output{Result: fmt.Sprintf("No troubleshooting guide for '%s'. Available: not_connecting, no_readings, wrong_values, battery_drain, offline, led_error, wifi_lost, mesh_disconnect, app_crash, firmware_update.", input.Issue)}, nil
			}

			return Output{Result: guide}, nil
		},
	)
}

func createGetMeshInfoTool() (tool.Tool, error) {
	type Input struct {
		Topic string `json:"topic"` // overview, root_node, esp_now, topology, range, troubleshooting
	}
	type Output struct {
		Result string `json:"result"`
	}

	meshGuides := map[string]string{
		"overview": `Mesh: ROOT (WiFi gateway) + NODEs (relay via ESP-NOW). Up to 20 ESP-NOW peers. Self-healing, auto-routing.
ROOT elected by WiFi RSSI (strongest signal), others auto-join as NODE. Only ROOT needs WiFi. NODEs save battery (no WiFi radio).`,

		"root_node": `ROOT = gateway to cloud. Always on WiFi. Battery ~40 days (vs ~21mo for NODEs). Elected by WiFi RSSI. Re-election 65s timeout. Min RSSI: -82 dBm.
Place near router. ROOT offline = all sensors lose cloud. Change ROOT: Device Settings → Mesh Role → Promote.`,

		"esp_now": `ESP-NOW: Espressif protocol over 2.4GHz. Range ~10m indoor/~50m outdoor. 1Mbps standard, LR 500k mode: ~500m range. AES-256-GCM app-level encryption.
Lower power than WiFi, no AP needed, works if WiFi down. Limit: 250 bytes/frame, 20 peers/node.`,

		"topology": `Star: all NODEs → ROOT (simple, limited range). Tree: NODEs relay through others (extended range, +latency). Hybrid: mixed.
Auto-optimized: sensors find best path, reroute in <5s if node drops. View: Settings → Mesh Network.`,

		"range": `Indoor: drywall ~10m, brick ~7m, concrete ~5m. Outdoor: open ~50m, garden ~30m.
RSSI: >-50=excellent, -50..-70=good, -70..-80=fair, <-80=poor (add relay node).
Extend: add intermediate nodes, position ROOT centrally, avoid metal walls.`,

		"troubleshooting": `NODE can't find ROOT: move closer, check ROOT online, factory reset NODE.
High latency: too many hops (max 3), add intermediate node.
Unstable: RSSI < -70dBm → move closer. Update firmware.
All down: check ROOT first, promote another sensor if needed.`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_mesh_info",
			Description: "Mesh network info. Topics: overview/root_node/esp_now/topology/range/troubleshooting.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_mesh_info called: topic=%s", input.Topic)

			topic := strings.ToLower(input.Topic)
			if topic == "" {
				topic = "overview"
			}

			guide, ok := meshGuides[topic]
			if !ok {
				return Output{Result: fmt.Sprintf("Unknown topic '%s'. Available: overview, root_node, esp_now, topology, range, troubleshooting.", input.Topic)}, nil
			}

			return Output{Result: guide}, nil
		},
	)
}

func createGetHardwareSpecsTool() (tool.Tool, error) {
	type Input struct {
		SpecType string `json:"specType"` // chip, sensor, battery, pins, wireless, all
	}
	type Output struct {
		Result string `json:"result"`
	}

	specs := map[string]string{
		"chip":     "CHIP: ESP32-C3, RISC-V single-core 160MHz, 400KB SRAM, 4MB Flash, WiFi 2.4GHz b/g/n + BLE 5.0.",
		"sensor":   "SENSOR: Capacitive soil moisture, ADC 12-bit, adc_dry=3500, adc_wet=1500, 5-sample median filter, ±3% accuracy.",
		"battery":  "BATTERY: LiPo 3.0-4.2V, 2000mAh, R1=R2=100kΩ divider, NODE ~21 months, ROOT ~40 days, deep sleep ~10μA, active ~80mA.",
		"pins":     "PINS: GPIO8=LED, GPIO9=button (hold 10s=factory reset), ADC1 CH0=humidity+battery, USB-C for charging+flashing.",
		"wireless": "WIRELESS: WiFi 2.4GHz b/g/n, BLE 5.0 (advertises as 'enddelIOT-XXXX'), ESP-NOW 1Mbps/LR 500kbps (~500m range), AES-256-GCM + ECDH P-256 + TLS 1.3.",
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_hardware_specs",
			Description: "Hardware specs: chip/sensor/battery/pins/wireless/all. ESP32-C3 details.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_hardware_specs called: specType=%s", input.SpecType)

			specType := strings.ToLower(input.SpecType)
			if specType == "" {
				specType = "all"
			}

			if specType == "all" {
				result := ""
				for _, key := range []string{"chip", "sensor", "battery", "pins", "wireless"} {
					result += specs[key] + "\n"
				}
				return Output{Result: result}, nil
			}

			spec, ok := specs[specType]
			if !ok {
				return Output{Result: fmt.Sprintf("Unknown specType '%s'. Available: chip, sensor, battery, pins, wireless, all.", input.SpecType)}, nil
			}

			return Output{Result: spec}, nil
		},
	)
}
