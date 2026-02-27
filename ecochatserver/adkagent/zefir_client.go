package adkagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
)

const (
	defaultZefirAPIURL = "https://zefirserver-production.up.railway.app"
	zefirClientTimeout = 15 * time.Second
)

// ZefirClient — HTTP client for Zefir IoT backend API
type ZefirClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// Device represents a Zefir sensor device
type Device struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	MacAddress  string    `json:"mac_address"`
	PlantName   string    `json:"plant_name"`
	PlantType   string    `json:"plant_type"`
	Status      string    `json:"status"` // online, offline, sleeping
	Battery     int       `json:"battery"`
	FirmwareVer string    `json:"firmware_version"`
	MeshRole    string    `json:"mesh_role"` // ROOT, NODE
	LastSeen    time.Time `json:"last_seen"`
	CreatedAt   time.Time `json:"created_at"`
}

// SensorReading represents a single sensor data point
type SensorReading struct {
	DeviceID    string    `json:"device_id"`
	Humidity    float64   `json:"humidity"`     // soil moisture %
	Temperature float64   `json:"temperature"`  // celsius
	Battery     int       `json:"battery"`      // %
	RSSI        int       `json:"rssi"`         // signal strength dBm
	Timestamp   time.Time `json:"timestamp"`
}

// DashboardData represents the user's dashboard summary
type DashboardData struct {
	TotalDevices  int              `json:"total_devices"`
	OnlineDevices int              `json:"online_devices"`
	Alerts        []DashboardAlert `json:"alerts"`
	Devices       []Device         `json:"devices"`
}

// DashboardAlert represents an alert on the dashboard
type DashboardAlert struct {
	DeviceID string `json:"device_id"`
	Type     string `json:"type"`    // low_moisture, low_battery, offline
	Message  string `json:"message"`
	Severity string `json:"severity"` // info, warning, critical
}

// MeshTopology represents the mesh network layout
type MeshTopology struct {
	RootNode  string     `json:"root_node"`
	Nodes     []MeshNode `json:"nodes"`
	TotalRSSI int        `json:"total_rssi"`
}

// MeshNode represents a node in the mesh network
type MeshNode struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Role     string `json:"role"` // ROOT, NODE
	ParentID string `json:"parent_id"`
	RSSI     int    `json:"rssi"`
	Status   string `json:"status"`
}

// NewZefirClient creates a new ZefirClient using DB settings
func NewZefirClient() *ZefirClient {
	baseURL := database.GetSetting("ZEFIR_API_URL", defaultZefirAPIURL)
	apiKey := database.GetSetting("ZEFIR_SERVICE_KEY", "")

	client := &http.Client{
		Timeout: zefirClientTimeout,
		Transport: &http.Transport{
			MaxIdleConns:    10,
			IdleConnTimeout: 30 * time.Second,
		},
	}

	log.Printf("[ZEFIR_CLIENT] Initialized: baseURL=%s, hasKey=%v", baseURL, apiKey != "")
	return &ZefirClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  client,
	}
}

// doRequest performs an authenticated HTTP request to the Zefir API
func (zc *ZefirClient) doRequest(ctx context.Context, method, path string) ([]byte, error) {
	url := zc.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZefirChat/1.0")
	if zc.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+zc.apiKey)
	}

	resp, err := zc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// GetUserDevices returns all devices for a user
func (zc *ZefirClient) GetUserDevices(ctx context.Context, userID string) ([]Device, error) {
	path := fmt.Sprintf("/api/devices?user_id=%s", userID)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get user devices: %w", err)
	}

	var devices []Device
	if err := json.Unmarshal(body, &devices); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}

	return devices, nil
}

// GetDeviceDetails returns details for a specific device
func (zc *ZefirClient) GetDeviceDetails(ctx context.Context, deviceID string) (*Device, error) {
	path := fmt.Sprintf("/api/devices/%s", deviceID)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get device details: %w", err)
	}

	var device Device
	if err := json.Unmarshal(body, &device); err != nil {
		return nil, fmt.Errorf("parse device: %w", err)
	}

	return &device, nil
}

// GetLatestReading returns the most recent sensor reading for a device
func (zc *ZefirClient) GetLatestReading(ctx context.Context, deviceID string) (*SensorReading, error) {
	path := fmt.Sprintf("/api/devices/%s/readings/latest", deviceID)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get latest reading: %w", err)
	}

	var reading SensorReading
	if err := json.Unmarshal(body, &reading); err != nil {
		return nil, fmt.Errorf("parse reading: %w", err)
	}

	return &reading, nil
}

// GetDeviceReadings returns historical readings for a device
func (zc *ZefirClient) GetDeviceReadings(ctx context.Context, deviceID string, from, to time.Time, limit int) ([]SensorReading, error) {
	path := fmt.Sprintf("/api/devices/%s/readings?from=%s&to=%s&limit=%d",
		deviceID,
		from.Format(time.RFC3339),
		to.Format(time.RFC3339),
		limit,
	)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get device readings: %w", err)
	}

	var readings []SensorReading
	if err := json.Unmarshal(body, &readings); err != nil {
		return nil, fmt.Errorf("parse readings: %w", err)
	}

	return readings, nil
}

// GetDashboard returns the user's dashboard summary
func (zc *ZefirClient) GetDashboard(ctx context.Context, userID string) (*DashboardData, error) {
	path := fmt.Sprintf("/api/dashboard?user_id=%s", userID)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get dashboard: %w", err)
	}

	var dashboard DashboardData
	if err := json.Unmarshal(body, &dashboard); err != nil {
		return nil, fmt.Errorf("parse dashboard: %w", err)
	}

	return &dashboard, nil
}

// GetMeshTopology returns the mesh network topology for a user
func (zc *ZefirClient) GetMeshTopology(ctx context.Context, userID string) (*MeshTopology, error) {
	path := fmt.Sprintf("/api/mesh/topology?user_id=%s", userID)
	body, err := zc.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("get mesh topology: %w", err)
	}

	var topology MeshTopology
	if err := json.Unmarshal(body, &topology); err != nil {
		return nil, fmt.Errorf("parse mesh topology: %w", err)
	}

	return &topology, nil
}

// maskMAC masks a MAC address for privacy: "AA:BB:CC:DD:EE:FF" → "AA:BB:...:EE:FF"
func maskMAC(mac string) string {
	parts := strings.Split(mac, ":")
	if len(parts) < 4 {
		return "XX:XX:...:XX"
	}
	return parts[0] + ":" + parts[1] + ":...:" + parts[len(parts)-2] + ":" + parts[len(parts)-1]
}

// FormatDevicesList formats a list of devices for display (with PII masking)
func FormatDevicesList(devices []Device) string {
	if len(devices) == 0 {
		return "No devices found."
	}

	result := fmt.Sprintf("Found %d device(s):\n\n", len(devices))
	for i, d := range devices {
		statusEmoji := "🟢"
		if d.Status == "offline" {
			statusEmoji = "🔴"
		} else if d.Status == "sleeping" {
			statusEmoji = "🟡"
		}

		result += fmt.Sprintf("%d. %s %s\n", i+1, statusEmoji, d.Name)
		if d.PlantName != "" {
			result += fmt.Sprintf("   Plant: %s", d.PlantName)
			if d.PlantType != "" {
				result += fmt.Sprintf(" (%s)", d.PlantType)
			}
			result += "\n"
		}
		result += fmt.Sprintf("   Battery: %d%% | Role: %s\n", d.Battery, d.MeshRole)
		if d.MacAddress != "" {
			result += fmt.Sprintf("   MAC: %s\n", maskMAC(d.MacAddress))
		}
		result += fmt.Sprintf("   Last seen: %s\n\n", d.LastSeen.Format("02.01.2006 15:04"))
	}

	return result
}

// FormatSensorReading formats a sensor reading for display with data validation
func FormatSensorReading(reading *SensorReading) string {
	result := "Sensor Reading:\n\n"

	// Validate and flag abnormal sensor data
	var warnings []string

	if reading.Humidity < 0 || reading.Humidity > 100 {
		warnings = append(warnings, fmt.Sprintf("⚠️ Moisture %.1f%% is out of range (0-100%%) — possible sensor malfunction. Try restarting the sensor.", reading.Humidity))
	}
	if reading.Temperature > 60 || reading.Temperature < -20 {
		warnings = append(warnings, fmt.Sprintf("⚠️ Temperature %.1f°C is abnormal — possible sensor error. Try recalibrating.", reading.Temperature))
	}

	result += fmt.Sprintf("   Soil Moisture: %.1f%%\n", reading.Humidity)
	result += fmt.Sprintf("   Temperature: %.1f°C\n", reading.Temperature)
	result += fmt.Sprintf("   Battery: %d%%\n", reading.Battery)
	result += fmt.Sprintf("   Signal: %d dBm\n", reading.RSSI)
	result += fmt.Sprintf("   Time: %s\n", reading.Timestamp.Format("02.01.2006 15:04:05"))

	if len(warnings) > 0 {
		result += "\n⚠️ DATA WARNINGS:\n"
		for _, w := range warnings {
			result += w + "\n"
		}
		result += "\nNote: Readings may be unreliable. Do NOT use for plant care decisions until sensor is verified."
	}

	return result
}
