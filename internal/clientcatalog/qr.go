package clientcatalog

import (
	"encoding/base64"
	"fmt"
	"strings"

	"rsc.io/qr"
)

func (s *Service) QRData(clientID, platform string) (string, string, error) {
	if _, _, err := s.find(clientID, platform); err != nil {
		return "", "", ErrNotFound
	}
	downloadURL := s.stableURL("client-link", clientID, platform, "qr")
	code, err := qr.Encode(downloadURL, qr.M)
	if err != nil {
		return "", "", fmt.Errorf("encode client download QR: %w", err)
	}
	const quietZone = 4
	viewSize := code.Size + 2*quietZone
	var svg strings.Builder
	svg.Grow(code.Size * code.Size * 4)
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="white"/><path fill="black" d="`, viewSize, viewSize)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&svg, "M%d %dh1v1h-1z", x+quietZone, y+quietZone)
			}
		}
	}
	svg.WriteString(`"/></svg>`)
	dataURL := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg.String()))
	return downloadURL, dataURL, nil
}
