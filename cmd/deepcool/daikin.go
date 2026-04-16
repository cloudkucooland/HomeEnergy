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

// this needs some serious DRY dont repeat yourself

const base = "https://integrator-api.daikinskyport.com"

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

func NewDaikin(email, developerKey, apiKey string) (*daikin, error) {
	d := &daikin{
		Email:        email,
		DeveloperKey: developerKey,
		APIKey:       apiKey,
	}

	payload := struct {
		Email           string `json:"email"`
		IntegratorToken string `json:"integratorToken"`
	}{
		Email:           email,
		IntegratorToken: developerKey,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth payload: %w", err)
	}

	url := fmt.Sprintf("%s/v1/token", base)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["x-api-key"] = []string{apiKey}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DeepCool/1.0 (+https://github.com/cloudkucooland/HomeEnergy)")

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth failed: %s", res.Status)
	}

	if err := json.NewDecoder(res.Body).Decode(&d.AccessToken); err != nil {
		return nil, fmt.Errorf("failed to decode access token: %w", err)
	}

	// Set expiration time locally (usually 900 seconds)
	d.AccessToken.ExpiresAt = time.Now().Add(time.Duration(d.AccessToken.ExpiresIn) * time.Second)

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

	slog.Info("Daikin token expired, renewing...")

	// this fails the DRY test ... but works for now, just call this from NewDaikin
	payload := struct {
		Email           string `json:"email"`
		IntegratorToken string `json:"integratorToken"`
	}{
		Email:           d.Email,
		IntegratorToken: d.DeveloperKey,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/v1/token", base)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))

	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("renewal failed: %s", res.Status)
	}

	if err := json.NewDecoder(res.Body).Decode(&d.AccessToken); err != nil {
		return "", err
	}

	d.AccessToken.ExpiresAt = time.Now().Add(time.Duration(d.AccessToken.ExpiresIn) * time.Second)
	slog.Info("Daikin token renewed", "expires_at", d.AccessToken.ExpiresAt)

	return d.AccessToken.Value, nil
}

func (d *daikin) SetDeepCool(deviceID int, heat, cool float64) error {
    if deviceID >= len(d.Devices) {
        return fmt.Errorf("device index %d out of range", deviceID)
    }

	token, err := d.GetToken()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/devices/%s/msp", base, d.Devices[deviceID].ID)

	payload := MSPPayload{
		Mode:         2, // cool
		HeatSetpoint: heat,
		CoolSetpoint: cool,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set auto mode: %s", res.Status)
	}

	slog.Info("Daikin set to Auto", "device", deviceID, "cool", cool, "heat", heat)
	return nil
}

func (d *daikin) SetSchedule(deviceID int) error {
    if deviceID >= len(d.Devices) {
        return fmt.Errorf("device index %d out of range", deviceID)
    }

	token, err := d.GetToken()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/devices/%s/schedule", base, d.Devices[deviceID].ID)

	payload := struct {
		ScheduleEnabled bool `json:"scheduleEnabled"`
	}{
		ScheduleEnabled: true,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to toggle schedule: %s", res.Status)
	}

	slog.Info("Daikin schedule status updated", "device", deviceID)
	return nil
}

func (d *daikin) getDevices() error {
	token, err := d.GetToken()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/devices", base)
	req, _ := http.NewRequest("GET", url, nil)

	req.Header["x-api-key"] = []string{d.APIKey}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Parse the nested structure
	var locations []Location
	if err := json.NewDecoder(res.Body).Decode(&locations); err != nil {
		return fmt.Errorf("failed to decode locations: %w", err)
	}

	// Flatten devices from all locations into our struct
	d.Devices = []Device{}
	for _, loc := range locations {
		d.Devices = append(d.Devices, loc.Devices...)
	}

	for _, dd := range d.Devices {
		slog.Info("Daikin device found", "name", dd.Name, "id", dd.ID, "model", dd.Model)
	}

	return nil
}
