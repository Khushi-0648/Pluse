package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	_ "modernc.org/sqlite"
	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsapp-automation/pkg/config"
)

// ClientStatus captures current connection and account status
type ClientStatus struct {
	Connected bool   `json:"connected"`
	LoggedIn  bool   `json:"logged_in"`
	JID       string `json:"jid,omitempty"`
	Phone     string `json:"phone,omitempty"`
	PushName  string `json:"push_name,omitempty"`
	LatestQR  string `json:"latest_qr,omitempty"`
	PairCode  string `json:"pair_code,omitempty"`
}

// MessageHandler is a callback function invoked when an incoming WhatsApp message is received
type MessageHandler func(evt *events.Message)

// LogoutHandler is invoked when the session is unlinked or reset
type LogoutHandler func()

// HistorySyncHandler is invoked when historical chats and messages are synced
type HistorySyncHandler func(evt *events.HistorySync)

// ConnectHandler is invoked when WhatsApp client connects and logs in
type ConnectHandler func()

// WhatsAppClient wraps the whatsmeow client with automation utilities
type WhatsAppClient struct {
	cfg       *config.Config
	container *sqlstore.Container
	device    *store.Device
	client    *whatsmeow.Client
	log       waLog.Logger

	mu                  sync.RWMutex
	resetMu             sync.Mutex
	latestQR            string
	pairCode            string
	connected           bool
	loggedIn            bool
	messageHandlers     []MessageHandler
	logoutHandlers      []LogoutHandler
	historySyncHandlers []HistorySyncHandler
	connectHandlers     []ConnectHandler
	cancelFn            context.CancelFunc
	qrCancelFn          context.CancelFunc
}

// NewWhatsAppClient initializes a new WhatsApp client with SQLite session store
func NewWhatsAppClient(cfg *config.Config) (*WhatsAppClient, error) {
	logLevel := strings.ToUpper(cfg.WhatsApp.LogLevel)
	if logLevel == "" {
		logLevel = "INFO"
	}
	log := waLog.Stdout("WhatsApp", logLevel, true)

	dbPath := cfg.WhatsApp.DBPath
	if dbPath == "" {
		dbPath = "whatsapp.db"
	}
	dbURI := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_busy_timeout=5000", dbPath)

	container, err := sqlstore.New(context.Background(), "sqlite", dbURI, log)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite store %s: %w", dbPath, err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get first device from store: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, log)
	client.AutoTrustIdentity = true

	isLoggedIn := deviceStore != nil && deviceStore.ID != nil

	w := &WhatsAppClient{
		cfg:       cfg,
		container: container,
		device:    deviceStore,
		client:    client,
		log:       log,
		loggedIn:  isLoggedIn,
	}

	// Register event listeners
	client.AddEventHandler(w.handleEvent)

	return w, nil
}

// OnMessage registers a handler function for incoming messages
func (w *WhatsAppClient) OnMessage(handler MessageHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messageHandlers = append(w.messageHandlers, handler)
}

// OnLogout registers a callback invoked when device is logged out or unlinked
func (w *WhatsAppClient) OnLogout(handler LogoutHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logoutHandlers = append(w.logoutHandlers, handler)
}

// OnHistorySync registers a callback invoked when historical chats and messages are synced
func (w *WhatsAppClient) OnHistorySync(handler HistorySyncHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.historySyncHandlers = append(w.historySyncHandlers, handler)
}

// OnConnect registers a callback invoked when client connects and authenticates
func (w *WhatsAppClient) OnConnect(handler ConnectHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.connectHandlers = append(w.connectHandlers, handler)
}

// GetAllContacts retrieves the synchronized contacts from the whatsmeow contact store
func (w *WhatsAppClient) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	w.mu.RLock()
	cli := w.client
	w.mu.RUnlock()
	if cli == nil || cli.Store == nil || cli.Store.Contacts == nil {
		return nil, fmt.Errorf("contact store unavailable")
	}
	return cli.Store.Contacts.GetAllContacts(ctx)
}

// GetContactName retrieves the display name (full name, push name, or business name) from whatsmeow store
func (w *WhatsAppClient) GetContactName(ctx context.Context, target string) string {
	w.mu.RLock()
	cli := w.client
	w.mu.RUnlock()
	if cli == nil || cli.Store == nil || cli.Store.Contacts == nil {
		return ""
	}

	target = strings.TrimSpace(target)
	if target == "" || target == "Me" || target == "CRM/Webhook" {
		return ""
	}

	clean := cleanPhoneNumber(target)
	if clean == "" {
		return ""
	}

	// Try with exact clean digits as user on DefaultUserServer and HiddenUserServer
	jidsToTry := []types.JID{
		types.NewJID(clean, types.DefaultUserServer),
		types.NewJID(clean, types.HiddenUserServer),
	}

	// If clean is an LID, try resolving to phone number and add that JID
	resolvedPN := w.ResolveString(ctx, clean)
	if resolvedPN != "" && resolvedPN != clean {
		jidsToTry = append(jidsToTry, types.NewJID(cleanPhoneNumber(resolvedPN), types.DefaultUserServer))
	}

	// If clean is a phone number, try resolving to LID and add that JID
	resolvedLID := w.ResolveLID(ctx, types.NewJID(clean, types.DefaultUserServer))
	if resolvedLID != "" && resolvedLID != clean {
		jidsToTry = append(jidsToTry, types.NewJID(resolvedLID, types.HiddenUserServer))
	}

	for _, jid := range jidsToTry {
		if jid.User == "" {
			continue
		}
		info, err := cli.Store.Contacts.GetContact(ctx, jid)
		if err == nil {
			name := strings.TrimSpace(info.FullName)
			if name == "" {
				name = strings.TrimSpace(info.PushName)
			}
			if name == "" {
				name = strings.TrimSpace(info.BusinessName)
			}
			if name == "" {
				name = strings.TrimSpace(info.FirstName)
			}
			if name != "" && name != "-" && !IsLID(name) {
				return name
			}
		}
	}

	return ""
}

// Start connects to WhatsApp servers and starts the session loop
func (w *WhatsAppClient) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	w.cancelFn = cancel

	if w.client.Store.ID == nil {
		// New login flow: QR or Pair Phone
		if w.cfg.WhatsApp.PairPhone != "" {
			go w.startPairPhoneFlow(ctx, w.cfg.WhatsApp.PairPhone)
		} else {
			go w.startQRLoginFlow(ctx)
		}
	} else {
		// Already logged in, connect directly
		w.mu.Lock()
		w.loggedIn = true
		w.mu.Unlock()

		w.log.Infof("Existing session found for JID: %s. Connecting...", w.client.Store.ID.String())
		err := w.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect with existing session: %w", err)
		}
	}

	return nil
}

// Stop closes the client connection and releases the SQLite database
func (w *WhatsAppClient) Stop() {
	w.mu.Lock()
	if w.qrCancelFn != nil {
		w.qrCancelFn()
		w.qrCancelFn = nil
	}
	w.mu.Unlock()

	if w.cancelFn != nil {
		w.cancelFn()
	}
	if w.client != nil {
		w.client.Disconnect()
	}
	if w.container != nil {
		_ = w.container.Close()
	}
}

// startQRLoginFlow monitors QR channel and displays QR code
func (w *WhatsAppClient) startQRLoginFlow(parentCtx context.Context) {
	w.mu.Lock()
	if w.qrCancelFn != nil {
		w.qrCancelFn()
	}
	qrCtx, cancel := context.WithCancel(parentCtx)
	w.qrCancelFn = cancel
	w.latestQR = ""
	cli := w.client
	w.mu.Unlock()

	defer cancel()

	if cli == nil {
		w.log.Errorf("Client is nil in startQRLoginFlow")
		return
	}

	if cli.IsConnected() {
		cli.Disconnect()
	}

	qrChan, err := cli.GetQRChannel(qrCtx)
	if err != nil {
		if !errors.Is(err, whatsmeow.ErrQRStoreContainsID) {
			w.log.Errorf("Failed to get QR channel: %v", err)
		}
		return
	}

	err = cli.Connect()
	if err != nil {
		w.log.Errorf("Failed to connect for QR flow: %v", err)
		return
	}

	for {
		select {
		case <-qrCtx.Done():
			return
		case evt, ok := <-qrChan:
			if !ok {
				// qrChan closed (timeout, disconnect, or ran out of codes)
				w.mu.Lock()
				w.latestQR = ""
				isLogged := w.loggedIn
				w.mu.Unlock()

				if !isLogged && qrCtx.Err() == nil {
					w.log.Warnf("WhatsApp QR channel closed or expired without login. Automatically requesting fresh QR in 2 seconds...")
					select {
					case <-time.After(2 * time.Second):
						w.mu.RLock()
						stillNotLogged := !w.loggedIn
						w.mu.RUnlock()
						if stillNotLogged && qrCtx.Err() == nil {
							go func() {
								_ = w.ResetSession(context.Background())
							}()
						}
					case <-qrCtx.Done():
					}
				}
				return
			}

			switch evt.Event {
			case "code":
				w.mu.Lock()
				w.latestQR = evt.Code
				w.mu.Unlock()

				fmt.Println()
				fmt.Println("=======================================================")
				fmt.Println("Scan this QR code with your WhatsApp mobile application:")
				fmt.Println("Settings > Linked Devices > Link a Device")
				fmt.Println("=======================================================")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("=======================================================")
				fmt.Println()

			case "timeout":
				w.mu.Lock()
				w.latestQR = ""
				w.mu.Unlock()
				w.log.Warnf("Current QR code timed out. Waiting for refreshed QR...")

			case "success":
				w.mu.Lock()
				w.loggedIn = true
				w.connected = true
				w.latestQR = ""
				w.mu.Unlock()
				w.log.Infof("Successfully authenticated via QR Code!")
				return

			default:
				if evt.Error != nil {
					w.log.Warnf("QR channel event '%s': %v", evt.Event, evt.Error)
				}
			}
		}
	}
}

// startPairPhoneFlow connects and requests an 8-character pairing code
func (w *WhatsAppClient) startPairPhoneFlow(ctx context.Context, phone string) {
	err := w.client.Connect()
	if err != nil {
		w.log.Errorf("Failed to connect for pair code: %v", err)
		return
	}

	cleanPhone := cleanPhoneNumber(phone)
	w.log.Infof("Requesting WhatsApp pairing code for phone: %s", cleanPhone)

	// Wait 1 second for socket readiness
	time.Sleep(1 * time.Second)

	code, err := w.client.PairPhone(ctx, cleanPhone, true, whatsmeow.PairClientChrome, "Chrome (Windows)")
	if err != nil {
		w.log.Errorf("PairPhone failed: %v", err)
		return
	}

	w.mu.Lock()
	w.pairCode = code
	w.mu.Unlock()

	fmt.Printf("\n=======================================================\n")
	fmt.Printf("WhatsApp 8-Digit Pairing Code: %s\n", code)
	fmt.Printf("Open WhatsApp on phone > Linked Devices > Link with phone number\n")
	fmt.Printf("=======================================================\n\n")
}

// handleEvent processes internal whatsmeow events
func (w *WhatsAppClient) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Connected:
		w.mu.Lock()
		w.connected = true
		w.loggedIn = true
		w.latestQR = ""
		w.pairCode = ""
		w.mu.Unlock()
		w.log.Infof("Connected to WhatsApp WebSocket servers!")

		// Update presence so server has your push name
		_ = w.client.SendPresence(context.Background(), types.PresenceAvailable)

		w.mu.RLock()
		cHandlers := make([]ConnectHandler, len(w.connectHandlers))
		copy(cHandlers, w.connectHandlers)
		w.mu.RUnlock()
		for _, h := range cHandlers {
			go h()
		}

	case *events.PairSuccess:
		w.mu.Lock()
		w.connected = true
		w.loggedIn = true
		w.latestQR = ""
		w.pairCode = ""
		w.mu.Unlock()
		w.log.Infof("Successfully authenticated with WhatsApp! JID: %s, Business: %s, Platform: %s", v.ID.String(), v.BusinessName, v.Platform)
		_ = w.client.SendPresence(context.Background(), types.PresenceAvailable)

		w.mu.RLock()
		cHandlers := make([]ConnectHandler, len(w.connectHandlers))
		copy(cHandlers, w.connectHandlers)
		w.mu.RUnlock()
		for _, h := range cHandlers {
			go h()
		}

	case *events.PairError:
		w.mu.Lock()
		w.pairCode = ""
		w.mu.Unlock()
		w.log.Errorf("WhatsApp pairing error: %v (business: %s, platform: %s)", v.Error, v.BusinessName, v.Platform)

	case *events.Disconnected:
		w.mu.Lock()
		w.connected = false
		w.mu.Unlock()
		w.log.Warnf("Disconnected from WhatsApp servers.")

	case *events.HistorySync:
		w.mu.RLock()
		hHandlers := make([]HistorySyncHandler, len(w.historySyncHandlers))
		copy(hHandlers, w.historySyncHandlers)
		w.mu.RUnlock()
		for _, h := range hHandlers {
			go h(v)
		}

	case *events.LoggedOut:
		w.mu.Lock()
		w.connected = false
		w.loggedIn = false
		w.latestQR = ""
		w.pairCode = ""
		w.mu.Unlock()
		w.log.Warnf("Device was unlinked/logged out from phone. Resetting session and starting fresh login flow...")

		w.mu.RLock()
		lHandlers := make([]LogoutHandler, len(w.logoutHandlers))
		copy(lHandlers, w.logoutHandlers)
		w.mu.RUnlock()
		for _, h := range lHandlers {
			go h()
		}

		go func() {
			time.Sleep(1 * time.Second)
			_ = w.ResetSession(context.Background())
		}()

	case *events.StreamReplaced:
		w.mu.Lock()
		w.connected = false
		w.mu.Unlock()
		w.log.Warnf("WhatsApp session stream was replaced from another connection.")

	case *events.Message:
		w.mu.RLock()
		handlers := make([]MessageHandler, len(w.messageHandlers))
		copy(handlers, w.messageHandlers)
		w.mu.RUnlock()

		for _, handler := range handlers {
			go handler(v)
		}
	}
}

// GetStatus returns the current connection and login status
func (w *WhatsAppClient) GetStatus() ClientStatus {
	if w == nil {
		return ClientStatus{
			Connected: false,
			LoggedIn:  false,
		}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	var jidStr, phoneStr string
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		jid := w.client.Store.ID
		jidStr = jid.String()
		phoneStr = jid.User
	}

	pushName := ""
	if w.client != nil && w.client.Store != nil {
		pushName = w.client.Store.PushName
		if pushName == "" {
			pushName = w.client.Store.BusinessName
		}
	}

	hasSession := w.client != nil && w.client.Store != nil && w.client.Store.ID != nil
	isLogged := hasSession || w.loggedIn || (w.client != nil && w.client.IsLoggedIn())
	isConn := w.connected || (isLogged && w.client != nil && w.client.IsConnected())

	qrToReturn := w.latestQR
	pairCodeToReturn := w.pairCode
	if isLogged {
		qrToReturn = ""
		pairCodeToReturn = ""
	}

	return ClientStatus{
		Connected: isConn,
		LoggedIn:  isLogged,
		JID:       jidStr,
		Phone:     phoneStr,
		PushName:  pushName,
		LatestQR:  qrToReturn,
		PairCode:  pairCodeToReturn,
	}
}

// IsConnected returns true if the client is currently connected
func (w *WhatsAppClient) IsConnected() bool {
	if w == nil {
		return false
	}
	return w.GetStatus().Connected
}

// IsLoggedIn returns true if the client is currently logged in
func (w *WhatsAppClient) IsLoggedIn() bool {
	if w == nil {
		return false
	}
	return w.GetStatus().LoggedIn
}

// ResolvePhoneNumber attempts to convert any JID or LID string into a real international phone number
func (w *WhatsAppClient) ResolvePhoneNumber(ctx context.Context, jid types.JID) string {
	if jid.Server == types.DefaultUserServer && jid.User != "" {
		return jid.User
	}
	lidJID := jid
	if lidJID.Server != types.HiddenUserServer {
		lidJID = types.NewJID(jid.User, types.HiddenUserServer)
	}
	if w.container != nil && w.container.LIDMap != nil {
		pn, err := w.container.LIDMap.GetPNForLID(ctx, lidJID)
		if err == nil && pn.User != "" {
			return pn.User
		}
	}
	if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
		pn, err := w.client.Store.LIDs.GetPNForLID(ctx, lidJID)
		if err == nil && pn.User != "" {
			return pn.User
		}
	}
	return jid.User
}

// ResolveString takes any phone string, JID string, or LID string and resolves it to a real international phone number
func (w *WhatsAppClient) ResolveString(ctx context.Context, target string) string {
	target = strings.TrimSpace(target)
	if target == "" || target == "Me" || target == "CRM/Webhook" {
		return target
	}
	if strings.Contains(target, "@") {
		parsed, err := types.ParseJID(target)
		if err == nil {
			return w.ResolvePhoneNumber(ctx, parsed)
		}
	}
	clean := cleanPhoneNumber(target)
	if len(clean) > 5 {
		lidJID := types.NewJID(clean, types.HiddenUserServer)
		resolved := w.ResolvePhoneNumber(ctx, lidJID)
		if resolved != "" && resolved != clean {
			return resolved
		}
	}
	return target
}

// ResolveLID attempts to find the associated LID for a phone number JID
func (w *WhatsAppClient) ResolveLID(ctx context.Context, jid types.JID) string {
	if jid.Server == types.HiddenUserServer && jid.User != "" {
		return jid.User
	}
	pnJID := jid
	if pnJID.Server != types.DefaultUserServer {
		pnJID = types.NewJID(jid.User, types.DefaultUserServer)
	}
	if w.container != nil && w.container.LIDMap != nil {
		lid, err := w.container.LIDMap.GetLIDForPN(ctx, pnJID)
		if err == nil && lid.User != "" {
			return lid.User
		}
	}
	if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
		lid, err := w.client.Store.LIDs.GetLIDForPN(ctx, pnJID)
		if err == nil && lid.User != "" {
			return lid.User
		}
	}
	return ""
}

// StoreLIDPNMapping saves an LID <-> Phone mapping in the store
func (w *WhatsAppClient) StoreLIDPNMapping(ctx context.Context, first, second types.JID) {
	if w.container != nil && w.container.LIDMap != nil {
		lid := first
		pn := second
		if lid.Server != types.HiddenUserServer && pn.Server == types.HiddenUserServer {
			lid, pn = second, first
		}
		_ = w.container.LIDMap.PutLIDMapping(ctx, lid, pn)
	}
	if w.client != nil {
		w.client.StoreLIDPNMapping(ctx, first, second)
	}
}

// FormatJID normalizes a phone number or raw JID into a valid types.JID
func (w *WhatsAppClient) FormatJID(target string) (types.JID, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return types.EmptyJID, errors.New("recipient target is empty")
	}

	if strings.Contains(target, "@") {
		return types.ParseJID(target)
	}

	clean := cleanPhoneNumber(target)
	if len(clean) < 7 {
		return types.EmptyJID, fmt.Errorf("invalid phone number: %s", target)
	}

	// 1. If target is an LID, resolve to its real phone number JID if mapped
	lidJID := types.NewJID(clean, types.HiddenUserServer)
	if w.container != nil && w.container.LIDMap != nil {
		pn, err := w.container.LIDMap.GetPNForLID(context.Background(), lidJID)
		if err == nil && pn.User != "" {
			return pn, nil
		}
	}
	if w.client != nil && w.client.Store != nil && w.client.Store.LIDs != nil {
		pn, err := w.client.Store.LIDs.GetPNForLID(context.Background(), lidJID)
		if err == nil && pn.User != "" {
			return pn, nil
		}
	}

	return types.NewJID(clean, types.DefaultUserServer), nil
}

// SendTextMessage sends a plain text message to a phone or JID with dual LID/PN fallback
func (w *WhatsAppClient) SendTextMessage(ctx context.Context, target string, text string) (string, error) {
	if w.client != nil && !w.client.IsConnected() {
		// Wait up to 5 seconds for connection if temporarily reconnecting
		_ = w.client.WaitForConnection(5 * time.Second)
	}

	jid, err := w.FormatJID(target)
	if err != nil {
		return "", err
	}

	msg := &waProto.Message{
		Conversation: proto.String(text),
	}

	resp, err := w.client.SendMessage(ctx, jid, msg)
	if err != nil {
		// Dual fallback: If sending to PN failed, try corresponding LID if known
		if jid.Server == types.DefaultUserServer {
			lidStr := w.ResolveLID(ctx, jid)
			if lidStr != "" {
				lidJID := types.NewJID(lidStr, types.HiddenUserServer)
				resp2, err2 := w.client.SendMessage(ctx, lidJID, msg)
				if err2 == nil {
					return resp2.ID, nil
				}
			}
		} else if jid.Server == types.HiddenUserServer {
			pnStr := w.ResolvePhoneNumber(ctx, jid)
			if pnStr != "" && pnStr != jid.User {
				pnJID := types.NewJID(pnStr, types.DefaultUserServer)
				resp2, err2 := w.client.SendMessage(ctx, pnJID, msg)
				if err2 == nil {
					return resp2.ID, nil
				}
			}
		}
		return "", fmt.Errorf("failed to send message to %s: %w", jid.String(), err)
	}

	return resp.ID, nil
}

// SendReply sends a quoted reply to an existing message
func (w *WhatsAppClient) SendReply(ctx context.Context, target string, text string, quotedMsgID string, quotedParticipant string, quotedMsg *waProto.Message) (string, error) {
	if w.client != nil && !w.client.IsConnected() {
		_ = w.client.WaitForConnection(5 * time.Second)
	}

	jid, err := w.FormatJID(target)
	if err != nil {
		return "", err
	}

	stanzaID := quotedMsgID
	var participantPtr *string
	if quotedParticipant != "" {
		participantPtr = &quotedParticipant
	}

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      &stanzaID,
				Participant:   participantPtr,
				QuotedMessage: quotedMsg,
			},
		},
	}

	resp, err := w.client.SendMessage(ctx, jid, msg)
	if err != nil {
		// Fallback to plain text if quoted reply fails
		w.log.Warnf("Quoted reply failed (%v), falling back to standard text...", err)
		return w.SendTextMessage(ctx, target, text)
	}

	return resp.ID, nil
}

// SendMediaMessage uploads and sends an image or document (PDF/DOC) attachment with an optional caption
func (w *WhatsAppClient) SendMediaMessage(ctx context.Context, target string, data []byte, fileName string, mimeType string, caption string) (string, error) {
	if w.client != nil && !w.client.IsConnected() {
		_ = w.client.WaitForConnection(5 * time.Second)
	}

	jid, err := w.FormatJID(target)
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		return w.SendTextMessage(ctx, target, caption)
	}

	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	var msg *waProto.Message

	if strings.HasPrefix(mimeType, "image/") {
		uploadResp, err := w.client.Upload(ctx, data, whatsmeow.MediaImage)
		if err != nil {
			return "", fmt.Errorf("failed to upload image: %w", err)
		}

		imageMsg := &waProto.ImageMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &uploadResp.FileLength,
		}
		if caption != "" {
			imageMsg.Caption = proto.String(caption)
		}
		msg = &waProto.Message{
			ImageMessage: imageMsg,
		}
	} else {
		// Document (PDF, docx, txt, etc.)
		uploadResp, err := w.client.Upload(ctx, data, whatsmeow.MediaDocument)
		if err != nil {
			return "", fmt.Errorf("failed to upload document: %w", err)
		}

		if fileName == "" {
			if strings.Contains(mimeType, "pdf") {
				fileName = "document.pdf"
			} else {
				fileName = "document"
			}
		}

		docMsg := &waProto.DocumentMessage{
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &uploadResp.FileLength,
		}
		if caption != "" {
			docMsg.Caption = proto.String(caption)
		}
		msg = &waProto.Message{
			DocumentMessage: docMsg,
		}
	}

	resp, err := w.client.SendMessage(ctx, jid, msg)
	if err != nil {
		// Dual LID/PN fallback
		if jid.Server == types.DefaultUserServer {
			lidStr := w.ResolveLID(ctx, jid)
			if lidStr != "" {
				lidJID := types.NewJID(lidStr, types.HiddenUserServer)
				resp2, err2 := w.client.SendMessage(ctx, lidJID, msg)
				if err2 == nil {
					return resp2.ID, nil
				}
			}
		} else if jid.Server == types.HiddenUserServer {
			pnStr := w.ResolvePhoneNumber(ctx, jid)
			if pnStr != "" && pnStr != jid.User {
				pnJID := types.NewJID(pnStr, types.DefaultUserServer)
				resp2, err2 := w.client.SendMessage(ctx, pnJID, msg)
				if err2 == nil {
					return resp2.ID, nil
				}
			}
		}
		return "", fmt.Errorf("failed to send media to %s: %w", jid.String(), err)
	}

	return resp.ID, nil
}


// SimulateTyping sends a typing presence indicator for the given duration
func (w *WhatsAppClient) SimulateTyping(ctx context.Context, target string, duration time.Duration) error {
	jid, err := w.FormatJID(target)
	if err != nil {
		return err
	}

	_ = w.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	select {
	case <-time.After(duration):
	case <-ctx.Done():
		return ctx.Err()
	}
	_ = w.client.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	return nil
}

// MarkAsRead marks specific messages as read
func (w *WhatsAppClient) MarkAsRead(ctx context.Context, chat types.JID, sender types.JID, ids []types.MessageID) error {
	return w.client.MarkRead(ctx, ids, time.Now(), chat, sender)
}

// RequestPairCode triggers an interactive phone pair code request
func (w *WhatsAppClient) RequestPairCode(ctx context.Context, phone string) (string, error) {
	clean := cleanPhoneNumber(phone)
	if len(clean) < 7 {
		return "", fmt.Errorf("invalid phone number: %s (must be at least 7 digits with country code)", phone)
	}

	if w.client == nil {
		return "", errors.New("whatsapp client is not initialized")
	}

	// Ensure client is connected before sending pair request
	if !w.client.IsConnected() {
		if err := w.client.Connect(); err != nil {
			return "", fmt.Errorf("failed to connect to WhatsApp: %w", err)
		}
		time.Sleep(1500 * time.Millisecond)
	}

	// First try with push notification
	code, err := w.client.PairPhone(ctx, clean, true, whatsmeow.PairClientChrome, "Chrome (Windows)")
	if err != nil {
		w.log.Warnf("PairPhone with push notification failed (%v), retrying without push notification...", err)
		code, err = w.client.PairPhone(ctx, clean, false, whatsmeow.PairClientChrome, "Chrome (Windows)")
		if err != nil {
			return "", fmt.Errorf("failed to request pairing code: %w", err)
		}
	}

	w.mu.Lock()
	w.pairCode = code
	w.mu.Unlock()

	w.log.Infof("Generated WhatsApp pairing code: %s for %s", code, clean)
	return code, nil
}

// ResetSession wipes any invalid credentials and initiates a fresh QR pairing flow
func (w *WhatsAppClient) ResetSession(ctx context.Context) error {
	w.resetMu.Lock()
	defer w.resetMu.Unlock()

	w.mu.Lock()
	if w.qrCancelFn != nil {
		w.qrCancelFn()
		w.qrCancelFn = nil
	}
	wasLoggedIn := w.loggedIn
	w.connected = false
	w.loggedIn = false
	w.latestQR = ""
	w.pairCode = ""
	cli := w.client
	dev := w.device
	w.mu.Unlock()

	if cli != nil {
		hasCredentials := dev != nil && dev.ID != nil
		if wasLoggedIn || cli.IsLoggedIn() || hasCredentials {
			w.log.Infof("Actively unlinking companion device from WhatsApp servers...")
			if !cli.IsConnected() {
				_ = cli.Connect()
				_ = cli.WaitForConnection(3 * time.Second)
			}
			if cli.IsConnected() {
				logoutCtx, logoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := cli.Logout(logoutCtx)
				logoutCancel()
				if err != nil {
					w.log.Warnf("WhatsApp server Logout request returned: %v, forcing local cleanup", err)
					cli.Disconnect()
					if dev != nil {
						_ = dev.Delete(ctx)
					}
				} else {
					w.log.Infof("Successfully sent remove-companion-device; device unlinked from phone's Linked Devices.")
				}
			} else {
				cli.Disconnect()
				if dev != nil {
					_ = dev.Delete(ctx)
				}
			}
		} else {
			cli.Disconnect()
			if dev != nil {
				_ = dev.Delete(ctx)
			}
		}
	} else if dev != nil {
		_ = dev.Delete(ctx)
	}

	if w.container == nil {
		return nil
	}

	device := w.container.NewDevice()

	w.mu.Lock()
	w.device = device
	w.client = whatsmeow.NewClient(device, w.log)
	w.client.AutoTrustIdentity = true
	w.client.AddEventHandler(w.handleEvent)
	w.mu.Unlock()


	// Only notify logout handlers if session was genuinely active/logged-in previously
	if wasLoggedIn {
		w.mu.RLock()
		lHandlers := make([]LogoutHandler, len(w.logoutHandlers))
		copy(lHandlers, w.logoutHandlers)
		w.mu.RUnlock()
		for _, h := range lHandlers {
			go h()
		}
	}

	go w.startQRLoginFlow(context.Background())
	return nil
}

// RegenerateQR explicitly forces a clean session reset and immediate QR generation
func (w *WhatsAppClient) RegenerateQR(ctx context.Context) error {
	return w.ResetSession(ctx)
}

// Logout explicitly unlinks the companion device from WhatsApp servers and initiates fresh QR flow
func (w *WhatsAppClient) Logout(ctx context.Context) error {
	return w.ResetSession(ctx)
}

// CleanPhoneNumber strips non-digit characters and normalizes local prefixes (exported)
func CleanPhoneNumber(phone string) string {
	return cleanPhoneNumber(phone)
}

// cleanPhoneNumber strips non-digit characters and normalizes local prefixes
func cleanPhoneNumber(phone string) string {
	hadPlus := strings.HasPrefix(strings.TrimSpace(phone), "+")
	reg := regexp.MustCompile(`[^0-9]`)
	clean := reg.ReplaceAllString(phone, "")
	// If starting with 0 and 11 digits (e.g. 09876543210), strip the trunk 0
	if strings.HasPrefix(clean, "0") && len(clean) == 11 {
		clean = clean[1:]
	}
	// If no explicit '+' country code was specified, 10 digits starting with 6, 7, 8, or 9 (standard Indian mobile) defaults to 91
	if !hadPlus && len(clean) == 10 && (clean[0] == '6' || clean[0] == '7' || clean[0] == '8' || clean[0] == '9') {
		clean = "91" + clean
	}
	return clean
}

// IsLID reports whether target represents a 13-18 digit WhatsApp Multi-Device Linked Device ID (LID)
func IsLID(target string) bool {
	clean := strings.TrimSpace(strings.TrimPrefix(target, "+"))
	if len(clean) < 13 || len(clean) > 18 {
		return false
	}
	for _, r := range clean {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

