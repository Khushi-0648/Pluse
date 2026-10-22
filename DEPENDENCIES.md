# Dependencies and update routine

## Direct dependencies

| Module | Purpose |
| --- | --- |
| `golang.org/x/crypto` | bcrypt password hashing |
| `golang.org/x/sys` | Windows DPAPI for the data-encryption key and the sealed access token |
| `gopkg.in/yaml.v3` | `config.yaml` |
| `github.com/coder/websocket` | Only for the `uismoke` browser test tool |

Since 2.0 Pulse talks to Meta's Graph API (WhatsApp Cloud API) with the Go standard library only. The WhatsApp Web library (`whatsmeow`), SQLite, protobuf and the QR-code libraries were removed with the linked-device connection. The dashboard uses no third-party JavaScript or CSS.

## Monthly routine

```bash
go get -u ./...
go mod tidy
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go test ./...
go run -tags uismoke ./cmd/uismoke
```

Meta releases a new Graph API version about every three months and supports each for roughly two years. The version Pulse uses by default is `config.DefaultGraphVersion` (`pkg/config/config.go`); an install can override it under Settings > WhatsApp connection > Advanced. Before raising the default, read Meta's changelog for the WhatsApp Cloud API, then check with a test number: Test connection, receiving a message, an automatic reply, STOP/START, a hand-over, a template send, and a delivered/read update in Reports.

Before a release, raise the version number in `pkg/version/version.go`. That file is the only place the version is set; the program, `VERSION.txt` and the zip name all take it from there.

## When sending breaks

1. Read the plain-language error in Pulse and the technical one in the console window (Meta's error code is printed).
2. Check Meta's status page and the WhatsApp Cloud API changelog.
3. If the token expired or was revoked, create a new system-user token and paste it in Settings > WhatsApp connection.
