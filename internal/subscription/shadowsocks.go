package subscription

import "encoding/json"

var sip008Ciphers = map[string]struct{}{
	"aes-128-gcm": {}, "aes-256-gcm": {}, "aes-192-gcm": {}, "chacha20-ietf-poly1305": {},
}

type sip008Server struct {
	ID         int64  `json:"id"`
	Remarks    string `json:"remarks"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

type sip008Response struct {
	Servers        []sip008Server `json:"servers"`
	BytesUsed      int64          `json:"bytes_used"`
	BytesRemaining int64          `json:"bytes_remaining"`
	Version        int            `json:"version"`
}

func renderShadowsocks(input RenderInput) (Response, error) {
	servers := make([]sip008Server, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		cipher := stringSetting(node.ProtocolSettings, "cipher")
		if _, supported := sip008Ciphers[cipher]; !supported {
			continue
		}
		servers = append(servers, sip008Server{
			ID: node.ID, Remarks: node.Name, Server: node.Host, ServerPort: node.Port, Password: node.Password, Method: cipher,
		})
	}
	bytesUsed := input.Account.TrafficUpload + input.Account.TrafficDownload
	body, err := json.Marshal(sip008Response{
		Servers: servers, BytesUsed: bytesUsed, BytesRemaining: input.Account.TransferEnable - bytesUsed, Version: 1,
	})
	if err != nil {
		return Response{}, err
	}
	return Response{Body: body, ContentType: "application/json", Headers: map[string]string{}}, nil
}
