// Package notify is the hook the bot uses to alert staff.
//
// Alerts play a native non-visual audio chime on the host machine,
// and are displayed inside the dashboard (the Needs attention list
// and inbox badge). Customer messages and phone numbers are never
// shown as Windows popups/toasts to protect customer privacy on
// shared or unattended screens.
package notify

// Notifier receives staff alerts.
type Notifier interface {
	Notify(title, body string)
}

// Desktop is the notifier used by the app.
type Desktop struct {
	enabled func() bool
}

// NewDesktop creates the notifier.
func NewDesktop(enabled func() bool) *Desktop { return &Desktop{enabled: enabled} }

// Notify plays a native non-visual audio chime on the host machine
// when alerts are enabled, without displaying any visual desktop popup.
func (d *Desktop) Notify(title, body string) {
	if d.enabled != nil && !d.enabled() {
		return
	}
	playChime()
}

// Test plays the audio chime to verify speaker output.
func (d *Desktop) Test() error {
	playChime()
	return nil
}
