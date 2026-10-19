package server

import (
	"bytes"
	"testing"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/config"
)

func TestDashboardTemplateExecution(t *testing.T) {
	cfg := config.DefaultConfig()
	data := map[string]any{
		"Status":            "Connecting...",
		"ClientStatus":      client.ClientStatus{},
		"Uptime":            "1m0s",
		"Rules":             cfg.Rules,
		"RulesCount":        len(cfg.Rules),
		"Contacts":          []any{},
		"ContactsCount":     0,
		"Tags":              []string{},
		"Templates":         []any{},
		"TemplatesCount":    0,
		"Scheduled":         []any{},
		"ScheduledCount":    0,
		"MessagesIn":        0,
		"MessagesOut":       0,
		"IsBroadcasting":    false,
		"BusinessHours":     cfg.BusinessHours,
		"AI":                cfg.AI,
		"Automation":        cfg.Automation,
		"Webhook":           cfg.Webhook,
		"ServerPort":        8085,
		"MemUsageMB":        "12.4 MB",
		"Goroutines":        10,
		"IsDefaultPassword": true,
	}

	var buf bytes.Buffer
	err := dashboardTmpl.Execute(&buf, data)
	if err != nil {
		t.Fatalf("Template execution failed: %v", err)
	}
	t.Logf("Rendered HTML length: %d bytes", buf.Len())
}

func TestLoginAndSetupTemplateExecution(t *testing.T) {
	var buf bytes.Buffer
	err := loginTmpl.Execute(&buf, map[string]any{"Error": "Sample error", "MustChange": true})
	if err != nil {
		t.Fatalf("Login template execution failed: %v", err)
	}

	buf.Reset()
	err = setupPasswordTmpl.Execute(&buf, map[string]any{"Error": "Mismatch error"})
	if err != nil {
		t.Fatalf("Setup password template execution failed: %v", err)
	}
}
