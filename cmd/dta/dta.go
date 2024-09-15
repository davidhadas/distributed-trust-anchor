package main

import (
	"fmt"
	"os"

	"github.com/davidhadas/distributed-trust-anchor/pkg/autonomous_peer"
)

func main() {
	psk := []byte("12345678901234567890123456789012")
	myAddr := os.Getenv("my_addr")
	if len(myAddr) == 0 {
		myAddr = "ibm.com"
	}

	myHost := autonomous_peer.NewHost("/ip4/0.0.0.0/tcp/8888", psk)
	fmt.Printf("Host: %s\t%s\n", myHost.GetID(), myHost.GetAddress())

	select {}
}
