package adkagent

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ============================================================================
// DEVICE TOOLS — 5 tools for Zefir IoT sensor management
// API-based (2 tools) + Static guides (3 tools)
// ============================================================================

// ZefirUserIDProvider provides the current user's Zefir ID for API calls
type ZefirUserIDProvider interface {
	GetZefirUserID() string
}

// CreateDeviceTools creates 5 device-related tools
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
			Description: "Get user's Zefir sensor devices with status, battery, plant assignment. Use when user asks 'show my devices', 'my sensors', 'device list', 'what sensors do I have'. Optional format: 'short' for names only, 'detailed' (default) for full info.",
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
			Description: "Get latest sensor reading for a device: soil humidity, temperature, battery, signal strength. Use when user asks 'what's the moisture level', 'check my sensor', 'how is my plant doing'. Requires deviceID.",
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
		"overview": `ZEFIR SENSOR SETUP OVERVIEW

Setting up your Zefir sensor takes about 5 minutes:

1. UNBOX — Remove sensor from packaging, note the MAC address on the sticker
2. BLE PAIRING — Open Zefir app, tap "Add Device", hold sensor button 3 sec
3. WiFi CONFIG — Select your WiFi network, enter password
4. MESH SETUP — If you have multiple sensors, configure mesh network
5. PLANT ASSIGN — Select your plant from 89+ species database
6. FIRST READING — Insert sensor probe into soil, wait 30 sec for first reading

Requirements:
- Zefir app (iOS/Android/Desktop)
- WiFi 2.4GHz network (5GHz not supported by ESP32-C3)
- Charged sensor (ships 80%+ charged)

Use get_setup_guide(step='ble_pairing') for detailed step-by-step.`,

		"unboxing": `STEP 1: UNBOXING YOUR ZEFIR SENSOR

In the box:
- 1x Zefir ESP32-C3 sensor module
- 1x Soil moisture probe (capacitive)
- 1x USB-C charging cable
- 1x Quick start card

Before starting:
1. Note the MAC address on the sensor sticker (format: XX:XX:XX:XX:XX:XX)
2. Ensure sensor is charged (LED = green when pressed)
3. Download Zefir app if you haven't already

LED indicators:
- GREEN blink = ready for pairing
- BLUE blink = BLE active
- RED blink = low battery
- WHITE solid = charging
- No LED = press button once to wake`,

		"ble_pairing": `STEP 2: BLE PAIRING

1. Open Zefir app → tap "+" or "Add Device"
2. Hold sensor button for 3 seconds until BLUE LED blinks
3. App will show "Zefir-XXXX" in nearby devices list
4. Tap your sensor name to connect
5. Wait for "Connected" confirmation (5-10 seconds)

Troubleshooting BLE:
- Ensure Bluetooth is ON on your phone
- Keep sensor within 1 meter during pairing
- If not found: press button again for 3 sec
- iOS: Check Settings → Zefir → Bluetooth permission
- Android: Location permission required for BLE scanning
- If still failing: hold button 10 sec to factory reset`,

		"wifi_config": `STEP 3: WiFi CONFIGURATION

After BLE pairing:
1. App will show "Configure WiFi" screen
2. Select your 2.4GHz WiFi network
3. Enter WiFi password
4. Tap "Connect"
5. Sensor LED turns GREEN when connected

Important:
- ESP32-C3 supports ONLY 2.4GHz WiFi (not 5GHz)
- WPA2/WPA3 supported
- Hidden networks: type SSID manually
- Max password length: 63 characters
- Sensor stores WiFi credentials in flash memory

If your router uses dual-band:
- Some routers merge 2.4/5GHz under one name
- Try connecting from closer to router
- Or create separate 2.4GHz SSID in router settings`,

		"mesh_setup": `STEP 4: MESH NETWORK SETUP (for multiple sensors)

If you have 2+ Zefir sensors:
1. First sensor becomes ROOT node automatically
2. Additional sensors join as NODE
3. Mesh uses ESP-NOW protocol (no WiFi needed between nodes)
4. Only ROOT node needs WiFi — others relay through mesh

Setup:
1. Set up first sensor normally (it becomes ROOT)
2. Place additional sensors within 10m of any existing sensor
3. Add each sensor via app — they auto-discover mesh
4. Check topology in app: Settings → Mesh Network

Mesh benefits:
- Extended range: sensors relay data to each other
- Reduced power: NODE sensors don't need WiFi radio
- Self-healing: if one node drops, others reroute
- Up to 10 sensors per mesh network

Optimal placement:
- ROOT near WiFi router
- NODEs within 10m of at least one other sensor
- Avoid thick walls between sensors`,

		"plant_assign": `STEP 5: ASSIGN PLANT TO SENSOR

After sensor is connected:
1. Tap the sensor in your device list
2. Tap "Assign Plant"
3. Search from 89+ species database or type custom name
4. Select your plant — thresholds auto-configure!

What auto-configures:
- Soil moisture alert thresholds (min/max %)
- Temperature alert range
- Watering reminder frequency
- Plant-specific care tips

Custom plants:
- If your plant isn't in the database, tap "Custom"
- Set your own moisture thresholds
- Add a photo for the dashboard

You can change the plant assignment anytime.`,

		"first_reading": `STEP 6: FIRST SENSOR READING

1. Insert the sensor probe into soil
   - Depth: 2/3 of probe length
   - Angle: straight down or slight angle
   - Avoid rocks or very compacted soil
2. Wait 30 seconds for calibration
3. First reading appears on dashboard

Understanding readings:
- Soil Moisture: % (0=bone dry, 100=saturated)
- Temperature: soil temperature in Celsius
- Battery: remaining charge %
- Signal: RSSI in dBm (closer to 0 = better)

Reading frequency:
- Default: every 30 minutes
- Configurable: 15 min to 6 hours
- More frequent = more battery drain
- Recommended: 30 min for most plants`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_setup_guide",
			Description: "Get step-by-step setup guide for Zefir sensor. Steps: 'overview', 'unboxing', 'ble_pairing', 'wifi_config', 'mesh_setup', 'plant_assign', 'first_reading'. Use when user asks about setting up sensors, pairing, WiFi config, etc.",
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
		"not_connecting": `TROUBLESHOOTING: Sensor Won't Connect

1. Check battery — press button, look for LED
   - No LED: charge via USB-C for 30 min, try again
   - RED blink: low battery, charge first

2. Reset BLE pairing:
   - Hold button 3 sec until BLUE blink
   - In app: pull down to refresh device list
   - Keep phone within 1 meter

3. Still not connecting:
   - Force close Zefir app, reopen
   - Toggle Bluetooth OFF/ON on phone
   - iOS: Settings → Zefir → ensure Bluetooth ON
   - Android: ensure Location permission granted

4. Factory reset (last resort):
   - Hold button 10 seconds until all LEDs flash
   - Sensor resets to default settings
   - You'll need to set up again from scratch`,

		"no_readings": `TROUBLESHOOTING: No Sensor Readings

1. Check sensor probe connection:
   - Ensure probe is firmly inserted into sensor module
   - Check probe for physical damage

2. Check soil contact:
   - Probe must be in soil (not just sitting on surface)
   - Insert 2/3 of probe length
   - Very dry or very wet compacted soil may need loosening

3. Check sensor status in app:
   - Device should show "Online" status
   - If "Offline": see troubleshoot_device(issue='offline')

4. Reading interval:
   - Default is every 30 min — may need to wait
   - Go to Device Settings → Reading Interval to adjust
   - Tap "Read Now" for immediate reading

5. Restart sensor:
   - Press button once to wake
   - If still no readings, charge and retry`,

		"wrong_values": `TROUBLESHOOTING: Incorrect Sensor Values

Humidity reads 0% or 100%:
- 0%: Probe may not be in soil, or soil is extremely dry
- 100%: Sensor in water or extremely saturated soil
- Recalibrate: Remove from soil, dry probe, reinsert

Humidity seems too high/low:
- Capacitive sensors need 5 min to stabilize after insertion
- Different soil types have different baseline readings
- Sandy soil reads lower; clay soil reads higher
- Recalibrate in Device Settings → Calibrate

Temperature wrong:
- Soil temp differs from air temp (this is normal)
- Sensor measures SOIL temperature, not air
- Direct sunlight on sensor can affect readings
- Wait 10 min after inserting for accurate temp

Battery shows incorrect:
- Restart sensor (press button)
- Charge fully, then readings should normalize`,

		"battery_drain": `TROUBLESHOOTING: Fast Battery Drain

Normal battery life: 3-6 months (30-min reading interval)

Causes of fast drain:
1. Reading interval too frequent
   - 15 min: ~2 months
   - 30 min: ~4 months
   - 1 hour: ~6 months
   - Solution: increase interval in Device Settings

2. Weak WiFi signal
   - Sensor retries WiFi connection, drains battery
   - Check RSSI: should be > -70 dBm
   - Move sensor closer to router/mesh ROOT
   - Solution: use mesh network for distant sensors

3. Frequent mesh relaying
   - NODE sensors relay other sensors' data
   - More sensors in chain = more relay = more drain
   - Solution: position ROOT centrally

4. Firmware bug
   - Check for firmware updates in app
   - Settings → Device → Firmware Update

5. Cold temperatures
   - Battery performance drops below 5C
   - This is normal for all lithium batteries`,

		"offline": `TROUBLESHOOTING: Sensor Shows Offline

1. Check battery:
   - Press button — any LED response?
   - No LED: battery dead, charge via USB-C
   - Charge for at least 30 min before trying again

2. Check WiFi (ROOT sensors):
   - Is your WiFi network working?
   - Did you change WiFi password recently?
   - If password changed: factory reset and re-setup
   - Check router: 2.4GHz band enabled?

3. Check mesh connection (NODE sensors):
   - Is ROOT sensor online?
   - Are there sensors between NODE and ROOT?
   - Move sensor closer to nearest mesh node
   - Check mesh topology in app: Settings → Mesh

4. Range issues:
   - WiFi range: ~15m through walls
   - Mesh ESP-NOW range: ~10m through walls
   - Open space: up to 50m

5. Server issues:
   - Check zefir.app status page
   - If server down, sensor stores data locally
   - Data syncs when connection restores`,

		"led_error": `TROUBLESHOOTING: LED Error Codes

LED patterns:
- GREEN blink (1x): Normal operation, ready
- GREEN solid: Fully charged
- BLUE blink (fast): BLE pairing mode
- BLUE solid: BLE connected
- WHITE solid: Charging via USB-C
- RED blink (slow): Low battery (<15%)
- RED blink (fast): Error — see below
- RED solid: Critical error
- YELLOW blink: Firmware updating — DO NOT UNPLUG
- All colors flash: Factory reset in progress

RED fast blink errors:
1. WiFi connection failed — check credentials
2. Probe disconnected — reattach sensor probe
3. Memory error — restart sensor (press button)

RED solid error:
- Hardware fault
- Try: charge fully, then restart
- If persists: contact support for replacement`,

		"wifi_lost": `TROUBLESHOOTING: WiFi Connection Lost

Sensor keeps disconnecting from WiFi:

1. Signal strength:
   - Check RSSI in app (should be > -70 dBm)
   - Closer to 0 = stronger signal
   - < -80 dBm = too weak, move closer

2. Router issues:
   - Restart your router
   - Check 2.4GHz band is enabled
   - Some routers sleep 2.4GHz when no clients
   - Disable "band steering" if available

3. IP conflict:
   - Router may have too many devices
   - Assign static IP to sensor in router settings
   - Check DHCP lease pool isn't exhausted

4. WiFi password changed:
   - Sensor stores old credentials
   - Factory reset: hold button 10 sec
   - Re-setup with new credentials

5. Use mesh instead:
   - If WiFi is unreliable at sensor location
   - Place ROOT near router, use mesh for distant sensors`,

		"mesh_disconnect": `TROUBLESHOOTING: Mesh Network Issues

NODE sensor lost mesh connection:

1. Check ROOT sensor:
   - Is ROOT online and connected to WiFi?
   - ROOT is the gateway — if it's down, all NODEs lose connectivity

2. Range:
   - ESP-NOW range: ~10m indoors through walls
   - Check distance to nearest mesh node
   - Move sensor closer or add intermediate node

3. Interference:
   - Microwave ovens, some LED drivers can interfere
   - Move away from interference sources
   - Thick concrete/metal walls significantly reduce range

4. Topology:
   - Check mesh map in app: Settings → Mesh Network
   - Ensure no single point of failure
   - Star topology (all connect to ROOT) is less resilient
   - Chain/tree topology provides redundancy

5. Re-mesh:
   - Remove NODE from mesh: Device Settings → Leave Mesh
   - Place closer to ROOT
   - Re-add: it will auto-discover and rejoin`,

		"app_crash": `TROUBLESHOOTING: App Issues

Zefir app crashes or freezes:

1. Force close and reopen:
   - iOS: swipe up from bottom, swipe app away
   - Android: recent apps, swipe away
   - Desktop (Tauri): Cmd+Q / Alt+F4, reopen

2. Clear app cache:
   - iOS: delete and reinstall
   - Android: Settings → Apps → Zefir → Clear Cache
   - Desktop: delete ~/.zefir/cache/

3. Update app:
   - Check App Store / Play Store for updates
   - Desktop: app auto-updates, or download from zefir.app

4. Login issues:
   - Try logging out and back in
   - Check internet connection
   - Server status: zefir.app/status

5. Report bug:
   - In-app: Settings → Report Bug
   - Include: device model, OS version, steps to reproduce`,

		"firmware_update": `TROUBLESHOOTING: Firmware Update

How to update sensor firmware:

1. Open Zefir app → Device Settings → Firmware
2. If update available, tap "Update"
3. Sensor LED turns YELLOW during update
4. DO NOT remove battery or power during update!
5. Update takes 1-3 minutes
6. Sensor restarts automatically after update

If update fails:
- Ensure battery is > 30%
- Keep phone close to sensor during OTA
- Ensure stable WiFi connection
- Try again — OTA updates are resumable

If sensor is bricked after failed update:
- Connect USB-C cable to computer
- Download Zefir Flash Tool from zefir.app/tools
- Follow manual flashing instructions
- This resets to latest stable firmware`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "troubleshoot_device",
			Description: "Troubleshoot common Zefir sensor issues. Issues: 'not_connecting', 'no_readings', 'wrong_values', 'battery_drain', 'offline', 'led_error', 'wifi_lost', 'mesh_disconnect', 'app_crash', 'firmware_update'. Use when user reports sensor problems.",
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
		"overview": `ZEFIR MESH NETWORK OVERVIEW

Zefir sensors form a wireless mesh network using ESP-NOW protocol:

- ROOT node: connects to WiFi and relays all data to cloud
- NODE sensors: connect to mesh, relay data through other nodes to ROOT
- Self-healing: if one node drops, others automatically reroute
- No extra hardware needed — mesh is built into every sensor

Benefits:
- Extended range beyond WiFi coverage
- Better battery life for NODE sensors (no WiFi radio needed)
- Automatic failover and re-routing
- Up to 10 sensors per mesh network

How it works:
1. First sensor → becomes ROOT (connects to WiFi)
2. Additional sensors → auto-join as NODEs
3. NODEs send readings via ESP-NOW to nearest mesh neighbor
4. Data hops through mesh until it reaches ROOT
5. ROOT sends all data to Zefir cloud via WiFi`,

		"root_node": `MESH ROOT NODE

The ROOT node is the gateway between mesh and internet:

Properties:
- Always connected to WiFi
- Receives data from all NODE sensors
- Forwards everything to Zefir cloud
- Higher power consumption (WiFi + mesh radio)
- Battery life: ~2 months (vs 4-6 for NODEs)

Best practices:
- Place ROOT near your WiFi router for strong signal
- Keep ROOT charged or near USB power
- Assign ROOT to a plant near the router
- ROOT going offline = ALL sensors lose cloud connectivity

Changing ROOT:
1. Device Settings → Mesh Role → "Promote to ROOT"
2. Old ROOT automatically demotes to NODE
3. New ROOT must be in WiFi range`,

		"esp_now": `ESP-NOW PROTOCOL

Zefir mesh uses ESP-NOW — Espressif's proprietary protocol:

Technical details:
- Protocol: ESP-NOW over 802.11
- Frequency: 2.4 GHz (same as WiFi but independent channel)
- Range: ~10m indoors, ~50m outdoors (line of sight)
- Data rate: 1 Mbps
- Latency: <10ms per hop
- Encryption: CCMP (same as WPA2)
- Max peers: 20 per node (10 encrypted)

Advantages over WiFi:
- Much lower power consumption
- No need for WiFi infrastructure for NODE sensors
- Faster connection (no association/authentication)
- Works even if WiFi is down (for mesh communication)
- Peer-to-peer — no access point needed

Limitations:
- Shorter range than WiFi
- 250 bytes max payload per frame
- 2.4GHz only (subject to interference)`,

		"topology": `MESH TOPOLOGY

Zefir supports these mesh layouts:

1. STAR (default):
   ROOT in center, all NODEs connect directly
   [N]--[ROOT]--[N]
         |
        [N]
   Pro: Simple, low latency
   Con: Limited range (all must reach ROOT)

2. TREE/CHAIN:
   NODEs relay through other NODEs
   [N]--[N]--[ROOT]--[N]--[N]
   Pro: Extended range
   Con: More hops = slightly more latency

3. HYBRID:
   Mix of direct and relayed connections
   [N]--[N]--[ROOT]--[N]
              |
             [N]--[N]
   Pro: Best of both worlds
   Con: More complex routing

Zefir auto-optimizes:
- Sensors automatically find best path
- If a node drops, others reroute in <5 seconds
- View current topology: App → Settings → Mesh Network`,

		"range": `MESH NETWORK RANGE

ESP-NOW range depends on environment:

Indoor (through walls):
- Drywall: ~10m
- Brick: ~7m
- Concrete: ~5m
- Reinforced concrete: ~3m

Outdoor (line of sight):
- Open field: ~50m
- Garden with plants: ~30m
- Through trees: ~20m

Extending range:
1. Add intermediate sensor nodes
2. Position ROOT centrally
3. Avoid placing sensors behind metal objects
4. Elevate sensors slightly (don't place on ground)
5. External antenna mod (advanced, voids warranty)

RSSI guidelines:
- > -50 dBm: Excellent
- -50 to -70 dBm: Good
- -70 to -80 dBm: Fair (may have occasional drops)
- < -80 dBm: Poor (consider adding relay node)`,

		"troubleshooting": `MESH TROUBLESHOOTING

Common mesh issues:

1. NODE can't find ROOT:
   - Move closer to ROOT or another NODE
   - Check ROOT is online (green LED, WiFi connected)
   - Factory reset NODE and re-add to mesh

2. High latency / delayed readings:
   - Too many hops (max recommended: 3)
   - Add intermediate node to shorten path
   - Check for interference (microwaves, other 2.4GHz)

3. Unstable connections:
   - Check RSSI between nodes (> -70 dBm recommended)
   - Reduce distance or add relay nodes
   - Update firmware on all sensors

4. One NODE offline, others fine:
   - Check that NODE's battery isn't dead
   - Move closer to mesh network
   - Check for physical obstacles added recently

5. Entire mesh down:
   - Check ROOT sensor first
   - If ROOT is dead/offline, promote another sensor
   - Restart ROOT: press button, wait 30 sec`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_mesh_info",
			Description: "Get information about Zefir mesh networking: ESP-NOW protocol, ROOT/NODE roles, topology, range. Topics: 'overview', 'root_node', 'esp_now', 'topology', 'range', 'troubleshooting'. Use when user asks about mesh, network setup, signal, range.",
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
