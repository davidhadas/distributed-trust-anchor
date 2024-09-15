package main

import (
	"fmt"
	"time"

	"github.com/davidhadas/distributed-trust-anchor/pkg/autonomous_peer"
)

func main() {
	var firstHost *autonomous_peer.P2pHost
	//var previousHost *autonomous_peer.P2pHost

	psk := []byte("12345678901234567890123456789012")

	for i := range 100 {
		h := autonomous_peer.NewHost("/ip4/127.0.0.1/tcp/0", psk)
		fmt.Printf("Host %d: %s\t%s\n", i, h.GetID(), h.GetAddress())

		/*
			if previousHost != nil {
				h.AddPeers([]string{previousHost.Address}, uint64(i*10))
			}
			previousHost = h
			if firstHost == nil {
				firstHost = h
			}
		*/

		// test
		if firstHost == nil {
			firstHost = h
		} else {
			h.AddPeers(map[string]*autonomous_peer.IdRec{firstHost.GetHostAddress(): {Id: firstHost.GetID(), Version: time.Now()}})
		}
	}
	//firstHost.Close()
	time.Sleep(time.Minute * 5)
	firstHost.PurgePeer(firstHost.GetHostAddress())
	time.Sleep(time.Hour)
	fmt.Println("Finished")
}
