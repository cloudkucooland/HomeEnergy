package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	// "io"
	"log/slog"
	"net/http"
	"time"
)

const base = "https://integrator-api.daikinskyport.com/v1"

const modeCool = 2

type AccessToken struct {
	Value     string    `json:"accessToken"`
	ExpiresIn int       `json:"accessTokenExpiresIn"`
	TokenType string    `json:"tokenType"`
	ExpiresAt time.Time // Calculated locally
}

type daikin struct {
	Email        string
	DeveloperKey string // Developer API Key (from Developer Menu)
	APIKey       string // Integrator Token (from Home Integration menu)
	AccessToken  AccessToken
	Devices      []Device
}

type Location struct {
	LocationName string   `json:"locationName"`
	Devices      []Device `json:"devices"`
}

type Device struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Model           string `json:"model"`
	FirmwareVersion string `json:"firmwareVersion"`
}

type MSPPayload struct {
	Mode         int     `json:"mode"` // 1: Heat, 2: Cool, 3: Auto, 0: Off
	HeatSetpoint float64 `json:"heatSetpoint"`
	CoolSetpoint float64 `json:"coolSetpoint"`
}

type Info struct {
	EquipmentStatus     int     `json:"equipmentStatus"`
	Mode                int     `json:"mode"`
	ModeLimit           int     `json:"modeLimit"`
	ModeEMHeatAvailable bool    `json:"modeEmHeatAvailable"`
	Fan                 int     `json:"fan"`
	FanCiruclate        int     `json:"fanCirculate"`
	FanCiruclateSpeed   int     `json:"fanCirculateSpeed"`
	HeatSetpoint        float64 `json:"heatSetpoint"`
	CoolSetpoint        float64 `json:"coolSetpoint"`
	SetPointDelta       int     `json:"setpointDelta"`
	SetPointMinimum     float64 `json:"setpointMinimum"`
	SetPointMaximum     float64 `json:"setpointMaximum"`
	IndoorTemp          float64 `json:"tempIndoor"`
	IndoorHumidity      int     `json:"humIndoor"`
	OutdoorTemp         float64 `json:"tempOutdoor"`
	OutdoorHumidity     int     `json:"humOutdoor"`
	ScheduleEnabled     bool    `json:"scheduleEnabled"`
	GeofencingEnabled   bool    `json:"geofencingEnabled"`
}

func NewDaikin(email, developerKey, apiKey string) (*daikin, error) {
	d := &daikin{
		Email:        email,
		DeveloperKey: developerKey,
		APIKey:       apiKey,
	}

	d.refreshToken()
	slog.Info("Daikin authenticated", "expires_at", d.AccessToken.ExpiresAt)

	if err := d.getDevices(); err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}
	return d, nil
}

// GetToken returns the current token, or fetches a new one if it's expired
func (d *daikin) GetToken() (string, error) {
	// Give ourselves a 60-second buffer so we don't expire mid-request
	if time.Now().Add(60 * time.Second).Before(d.AccessToken.ExpiresAt) {
		return d.AccessToken.Value, nil
	}
	d.refreshToken()
	slog.Info("Daikin token renewed", "expires_at", d.AccessToken.ExpiresAt)
	return d.AccessToken.Value, nil
}

func (d *daikin) SetDeepCool(deviceID int, heat, cool float64) error {
	if deviceID >= len(d.Devices) {
		return fmt.Errorf("device index %d out of range", deviceID)
	}

	url := fmt.Sprintf("/devices/%s/msp", d.Devices[deviceID].ID)

	payload := MSPPayload{
		Mode:         2, // cool
		HeatSetpoint: heat,
		CoolSetpoint: cool,
	}

	body, _ := json.Marshal(payload)
	res, err := d.doRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set auto mode: %s", res.Status)
	}

	slog.Info("Daikin set to deep cool", "device", deviceID, "cool", cool, "heat", heat)
	return nil
}

func (d *daikin) SetSchedule(deviceID int) error {
	if deviceID >= len(d.Devices) {
		return fmt.Errorf("device index %d out of range", deviceID)
	}

	url := fmt.Sprintf("/devices/%s/schedule", d.Devices[deviceID].ID)
	payload := struct {
		ScheduleEnabled bool `json:"scheduleEnabled"`
	}{
		ScheduleEnabled: true,
	}

	body, _ := json.Marshal(payload)
	res, err := d.doRequest("PUT", url, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to toggle schedule: %s", res.Status)
	}

	slog.Info("Daikin set to Schedule", "device", deviceID)
	return nil
}

func (d *daikin) getDevices() error {
	res, err := d.doRequest("GET", "/devices", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var locations []Location
	if err := json.NewDecoder(res.Body).Decode(&locations); err != nil {
		return fmt.Errorf("failed to decode locations: %w", err)
	}

	d.Devices = []Device{}
	for _, loc := range locations {
		d.Devices = append(d.Devices, loc.Devices...)
	}

	for _, dd := range d.Devices {
		slog.Info("Daikin device found", "name", dd.Name, "id", dd.ID, "model", dd.Model)
	}
	return nil
}

func (d *daikin) GetInfo(deviceID int) (*Info, error) {
	url := fmt.Sprintf("/devices/%s", d.Devices[deviceID].ID)
	res, err := d.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var info Info
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode locations: %w", err)
	}

	// slog.Info("Daikin data pulled", "indoor temp", info.IndoorTemp, "indoor humidity", info.IndoorHumidity)
	return &info, nil
}

func (d *daikin) doRequest(method, path string, body interface{}) (*http.Response, error) {
	token, err := d.GetToken()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}

	url := fmt.Sprintf("%s%s", base, path)
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		return nil, err
	}

	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DeepCool/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

func (d *daikin) refreshToken() error {
	payload := struct {
		Email           string `json:"email"`
		IntegratorToken string `json:"integratorToken"`
	}{
		Email:           d.Email,
		IntegratorToken: d.DeveloperKey,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/token", base)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("auth failed: %s", res.Status)
	}

	if err := json.NewDecoder(res.Body).Decode(&d.AccessToken); err != nil {
		return err
	}

	d.AccessToken.ExpiresAt = time.Now().Add(time.Duration(d.AccessToken.ExpiresIn) * time.Second)
	return nil
}
