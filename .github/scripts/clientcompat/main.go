package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/subscription"
)

func main() {
	output := flag.String("output", "", "fixture output directory")
	flag.Parse()
	if *output == "" || len(flag.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "usage: clientcompat -output DIR MIHOMO_VERSION...")
		os.Exit(2)
	}
	if err := os.MkdirAll(*output, 0o700); err != nil {
		fatalf("create output directory: %v", err)
	}

	expires := time.Unix(1_900_000_000, 0)
	account := store.SubscriptionAccount{
		UUID: "11111111-2222-4333-8444-555555555555", TransferEnable: 10 << 30,
		ExpiredAt: &expires,
	}
	node := subscription.PreparedNode{
		ID: 91, Type: "mieru", Name: "Mieru TCP", Host: "mieru.example.test", Port: 443,
		Ports: "443-445", Password: account.UUID,
		ProtocolSettings: map[string]any{"transport": "tcp", "traffic_pattern": ""},
	}
	for _, version := range flag.Args() {
		response, err := subscription.Render(subscription.RenderInput{
			Account: account,
			Nodes:   []subscription.PreparedNode{node},
			Client:  subscription.ClientInfo{Kind: subscription.KindClashMeta, Name: "meta", Version: version},
			AppName: "Xboard Client Compatibility",
		})
		if err != nil {
			fatalf("render Mihomo %s Mieru fixture: %v", version, err)
		}
		path := filepath.Join(*output, "clashmeta-mieru-"+version+".yaml")
		if err := os.WriteFile(path, response.Body, 0o600); err != nil {
			fatalf("write %s: %v", path, err)
		}
	}
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
