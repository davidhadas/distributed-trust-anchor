package autonomous_peer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/davidhadas/distributed-trust-anchor/pkg/referencevals"
	"github.com/davidhadas/distributed-trust-anchor/pkg/trusteekeys"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/multiformats/go-multiaddr"
)

type IdRec struct {
	Id      string    `json:"id"`
	Version time.Time `json:"ver"`
}

type P2pHost struct {
	address          string
	hostAddr         string
	id               string
	host             host.Host
	quit             chan struct{}
	mu               sync.Mutex
	plist            []string              // []<host addr>
	peerHosts        map[string]*IdRec     // [<host addr>]<peer id>
	dataTypeVersions map[string]*time.Time // [<data type>]<time updated>
	Data             Data
}

type peerMessage struct {
	DataTypeVersions map[string]*time.Time `json:"versions"`
	PeerHosts        map[string]*IdRec     `json:"nodes"`
}

type dataMessage struct {
	DataType   string     `json:"type"`
	Version    *time.Time `json:"version"`
	DataLength int        `json:"len"`
}

const (
	API           = "/exchange"
	MAX_MESSAGE   = 1000000 //1M
	MESSAGE_PEERS = "Peers"
	MESSAGE_END   = "End"
)

func init() {
	// force private network
	pnet.ForcePrivateNetwork = true
}

func CreateAddress(addr, id string) string {
	return addr + "/p2p/" + id
}

func NewHost(myAddr string, psk []byte) *P2pHost {
	host, err := libp2p.New(
		libp2p.ListenAddrStrings(myAddr),
		libp2p.PrivateNetwork(psk),
	)
	if err != nil {
		panic(err)
	}

	id := host.ID().String()
	//host.Peerstore().Close()
	hostAddr := host.Addrs()[0].String()
	addr := CreateAddress(hostAddr, id)

	myHost := &P2pHost{
		host:             host,
		address:          addr,
		hostAddr:         hostAddr,
		id:               id,
		quit:             make(chan struct{}),
		peerHosts:        map[string]*IdRec{}, // map[string]*IdRec{hostAddr: {Id: id, Version: time.Now()}},
		dataTypeVersions: map[string]*time.Time{},
		Data:             Data{dataMap: make(map[string][]byte), dataUpdateMap: make(map[string]func([]byte))},
	}
	myHost.Data.RegisterDataUpdate("refVals", referencevals.ReferenceVals.Update)
	myHost.Data.RegisterDataUpdate("trusteeKeys", trusteekeys.TrusteeKeys.Update)

	host.SetStreamHandler(API, myHost.handlePeerRequest)

	go func() {
		x := 2000000000
		ticker := time.NewTicker(time.Duration(rand.Int() % x))
		for {
			select {
			case <-ticker.C:

				myHost.sendToNextPeer()
				ticker.Reset(time.Duration(rand.Int() % x))
				//x = int(1.1 * float64(x))
			case <-myHost.quit:
				ticker.Stop()
				myHost.host.Close()
				return
			}
		}
	}()

	return myHost
}

func (h *P2pHost) GetID() string {
	return h.id
}

func (h *P2pHost) GetAddress() string {
	return h.address
}

func (h *P2pHost) GetHostAddress() string {
	return h.hostAddr
}

// PurgePeer of a given hostAddr.
// Protected with host mutex.
func (h *P2pHost) PurgePeer(hostAddr string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	idRec, ok := h.peerHosts[hostAddr]
	if ok {
		idRec.Version = time.Now()
		if idRec.Id == "" {
			return
		}
		idRec.Id = ""
		peerId, err := peer.Decode(idRec.Id)
		if err == nil {
			h.host.Peerstore().ClearAddrs(peerId)
			h.host.Peerstore().RemovePeer(peerId)
			h.host.Peerstore().RemoveProtocols(peerId)
		}

	} else { // new record
		h.peerHosts[hostAddr] = &IdRec{Id: "", Version: time.Now()}
	}

}

// AddPeers using a map of peers.
// Protected with host mutex.
func (h *P2pHost) AddPeers(peerHosts map[string]*IdRec) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for hostAddr, idRec := range peerHosts {
		knownIdRec, iKnowThisHost := h.peerHosts[hostAddr]

		if hostAddr == h.hostAddr { // peer report on my own host
			if !iKnowThisHost {
				// should neve happen!
				log.Println("pmap is missing the host address! - Should never happen!!!")
				continue
			}
			if knownIdRec.Version.Before(idRec.Version) {
				if idRec.Id != "" {
					if idRec.Id == knownIdRec.Id {
						// update my record to the latest time
						knownIdRec.Version = idRec.Version
						continue
					}
					log.Println("peer reports a newer ID for my own host! - Should never happen!!!")
					knownIdRec.Version = time.Now() // fight it! teach others my own id!
					continue
				}
				h.peerHosts[hostAddr] = idRec
			}
			continue
		}

		// not our own hostAddr
		if !iKnowThisHost {
			// No such record!
			h.peerHosts[hostAddr] = idRec
			if idRec.Id != "" {
				addr := CreateAddress(hostAddr, idRec.Id)
				// Parse the multiaddr string.
				peerMA, err := multiaddr.NewMultiaddr(addr)
				if err != nil {
					panic(err)
				}
				peerId, err := peer.Decode(idRec.Id)
				if err == nil {
					h.host.Peerstore().AddAddrs(peerId, []multiaddr.Multiaddr{peerMA}, peerstore.PermanentAddrTTL)
				}
				// add it last such that it gets picked first for updates
				h.plist = append(h.plist, hostAddr)
			}
			continue
		}

		// not our own hostAddr and we already have a record for this peer
		if knownIdRec.Version.Before(idRec.Version) {
			// purge previous peer address
			if knownIdRec.Id != "" {
				peerId, err := peer.Decode(knownIdRec.Id)
				if err == nil {
					h.host.Peerstore().ClearAddrs(peerId)
					h.host.Peerstore().RemovePeer(peerId)
					h.host.Peerstore().RemoveProtocols(peerId)
				}
			}
			// change to new peer
			h.peerHosts[hostAddr] = idRec
			// add new peer address
			if idRec.Id != "" {
				addr := CreateAddress(hostAddr, idRec.Id)
				peerMA, err := multiaddr.NewMultiaddr(addr)
				if err != nil {
					panic(err)
				}
				peerId, err := peer.Decode(idRec.Id)
				if err == nil {
					h.host.Peerstore().AddAddrs(peerId, []multiaddr.Multiaddr{peerMA}, peerstore.PermanentAddrTTL)
				}
			}
		}
	}
	return
}

// sendPeerMessage.
// Protected with host mutex.
func (h *P2pHost) sendPeerMessage(s network.Stream) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	m := &peerMessage{
		DataTypeVersions: h.dataTypeVersions,
		PeerHosts:        h.peerHosts,
	}

	enc := json.NewEncoder(s)
	err := enc.Encode(m)
	return err
}

// SetDataVersion for a given dataType.
// Protected with host mutex.
func (h *P2pHost) SetDataVersion(dataType string, version *time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.dataTypeVersions[dataType] = version
}

// popNextPeer.
// Protected with host mutex.
func (h *P2pHost) popNextPeer() (nextHostAddr, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.plist) == 0 {
		numPeers := len(h.peerHosts)
		h.plist = make([]string, numPeers)
		i := 0
		for hostAddr, idRec := range h.peerHosts {
			if hostAddr == h.hostAddr || idRec.Id == "" {
				numPeers--
				h.plist = h.plist[:numPeers]
				continue
			}
			h.plist[i] = hostAddr
			i++
		}
		fmt.Printf("popNextPeer to %s - found %d peers\n", h.id, numPeers)
	}
	if len(h.plist) == 0 {
		return
	}
	nextHostAddr = h.plist[len(h.plist)-1]
	h.plist = h.plist[:len(h.plist)-1]
	//fmt.Printf("popNextPeer to %s - sending to service host %s (%d peers remaining) \n", h.hostAddr, nextHostAddr, len(h.plist))

	idRec, ok := h.peerHosts[nextHostAddr]
	if !ok {
		log.Printf("Error MISSING HOSTADDR: %s\n", nextHostAddr)
		return
	}
	if idRec == nil {
		log.Printf("Error MISSING idRec for HOSTADDR: %s\n", nextHostAddr)
		return
	}
	id = idRec.Id
	if id == "" {
		return
	}

	return
}

// listDataToSend given peer data type versions.
// Protected with host mutex.
func (h *P2pHost) listDataToSend(pDataVersions map[string]*time.Time) (dataTypeToSend []string, verToSend []*time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for verKey, verSince := range h.dataTypeVersions {
		pVerSince, pVerOk := pDataVersions[verKey]
		if !pVerOk || pVerSince.Before(*verSince) {
			dataTypeToSend = append(dataTypeToSend, verKey)
			verToSend = append(verToSend, verSince)
		}
	}
	return
}

func (h *P2pHost) Close() {
	close(h.quit)
}

// sendData for a given dataType.
func (h *P2pHost) sendData(s network.Stream, dataType string, version *time.Time) error {
	data := h.Data.GetData(dataType)
	m := &dataMessage{
		DataType:   dataType,
		Version:    version,
		DataLength: len(data),
	}
	enc := json.NewEncoder(s)
	err := enc.Encode(m)
	if err != nil {
		return err
	}
	n, err := s.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("Failed writing to data to peer - %d sent insetad of %d", n, len(data))
	}
	return nil
}

// sendEOF for a given dataType.
func (h *P2pHost) sendEOF(s network.Stream) error {
	m := &dataMessage{
		DataType: MESSAGE_END,
	}
	enc := json.NewEncoder(s)
	err := enc.Encode(m)
	return err
}

// readData for all dataTypes.
func (h *P2pHost) readData(lreader io.Reader) error {
	var m dataMessage

	for {
		if err := json.NewDecoder(lreader).Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if m.DataType == MESSAGE_END {
			break
		}

		var data []byte
		if m.DataLength > 0 {
			data = make([]byte, m.DataLength)
			n, err := lreader.Read(data)
			if err != nil {
				return err
			}
			if n != m.DataLength {
				return fmt.Errorf("Failed reading to data from peer - %d received insetad of %d", n, len(data))
			}
		} else {
			data = make([]byte, 0)
		}

		h.Data.SetData(m.DataType, data)
		h.SetDataVersion(m.DataType, m.Version)
	}

	return nil
}

// readPeerMessage
func (h *P2pHost) readPeerMessage(lreader io.Reader) (map[string]*time.Time, error) {
	var m peerMessage

	if err := json.NewDecoder(lreader).Decode(&m); err != nil {
		return nil, err
	}

	h.AddPeers(m.PeerHosts)

	return m.DataTypeVersions, nil
}

// handlePeerRequest
func (h *P2pHost) handlePeerRequest(s network.Stream) {
	var timer *time.Timer
	var once sync.Once

	timer = time.NewTimer(time.Second * 3)
	go func() {
		<-timer.C
		once.Do(func() {
			s.Reset()
		})
	}()
	lreader := io.LimitReader(bufio.NewReader(s), MAX_MESSAGE)

	pDataTypeVersions, err := h.readPeerMessage(lreader)
	if err != nil {
		log.Printf("Error during readPeerMessage in handlePeerRequest: %v/n", err)
		timer.Stop()
		once.Do(func() {
			s.Reset()
		})
		return
	}
	//log.Println("sendToNextPeer in handlePeerRequest - PeerMessage received")

	if err = h.sendPeerMessage(s); err != nil {
		log.Printf("Error sending peers in handlePeerRequest: %v\n", err)
		timer.Stop()
		once.Do(func() {
			s.Reset()
		})
		return
	}
	//log.Println("sendToNextPeer in handlePeerRequest - PeerMessage sent")

	dataTypes, dataTypeVersions := h.listDataToSend(pDataTypeVersions)
	if len(dataTypes) != len(dataTypeVersions) {
		log.Printf("Error listDataToSend from stream in handlePeerRequest: should never happen\n")
		timer.Stop()
		once.Do(func() {
			s.Reset()
		})
		return
	}

	for i := range dataTypes {
		if err := h.sendData(s, dataTypes[i], dataTypeVersions[i]); err != nil {
			log.Printf("Error sending data to stream in handlePeerRequest: %v\n", err)
			// We do not reset as we still wish to try to read
			break
		}
		//log.Printf("sendToNextPeer in handlePeerRequest - Data %s sent\n", dataTypes[i])
	}
	if err := h.sendEOF(s); err != nil {
		log.Printf("Error sending EOF to stream: %v\n", err)
		// We do not reset as we still wish to try to read
	}
	//log.Printf("sendToNextPeer in handlePeerRequest - EOF sent\n")

	if err := h.readData(lreader); err != nil {
		log.Printf("Error reading data from stream: %v\n", err)
	}

	//log.Println("sendToNextPeer in handlePeerRequest - Closing")
	timer.Stop()
	once.Do(func() {
		s.Close()
	})
}

func (h *P2pHost) sendToNextPeer() {
	var s network.Stream

	var streamOk bool
	var timer *time.Timer
	var once sync.Once

	for !streamOk {
		nextHostAddr, id := h.popNextPeer()
		if nextHostAddr == "" || id == "" {
			return
		}
		peerId, err := peer.Decode(id)
		if err != nil {
			log.Printf("Ilegal idRec.Id for HOSTADDR: %s\n", nextHostAddr)
			return
		}

		s, err = h.host.NewStream(context.Background(), peerId, API)
		if err != nil {
			log.Printf("Error creating a stream: %v\n", err)
			continue
		}
		timer = time.NewTimer(time.Second * 3)
		go func() {
			<-timer.C
			once.Do(func() {
				s.Reset()
			})
		}()

		err = h.sendPeerMessage(s)
		if err != nil {
			log.Printf("Error sending peers to stream: %v\n", err)
			timer.Stop()
			s.Reset()
			continue
		}
		//log.Println("sendToNextPeer  - PeerMessage sent")
		streamOk = true
	}
	if streamOk {
		lreader := io.LimitReader(bufio.NewReader(s), MAX_MESSAGE)
		pDataTypeVersions, err := h.readPeerMessage(lreader)
		if err != nil {
			log.Printf("Error reading from stream: %v\n", err)
			timer.Stop()
			once.Do(func() {
				s.Reset()
			})
			return
		}
		//log.Println("sendToNextPeer  - PeerMessage received")

		dataTypes, dataTypeVersions := h.listDataToSend(pDataTypeVersions)
		if len(dataTypes) != len(dataTypeVersions) {
			log.Printf("Error listDataToSend from stream: should never happen\n")
			timer.Stop()
			once.Do(func() {
				s.Reset()
			})
			return
		}

		for i := range dataTypes {
			if err := h.sendData(s, dataTypes[i], dataTypeVersions[i]); err != nil {
				log.Printf("Error sending data to stream: %v\n", err)
				// We do not reset as we still wish to try to read
				break
			}
			//log.Printf("sendToNextPeer  - Data %s sent\n", dataTypes[i])
		}
		if err := h.sendEOF(s); err != nil {
			log.Printf("Error sending EOF to stream: %v\n", err)
			// We do not reset as we still wish to try to read
		}
		//log.Printf("sendToNextPeer  - EOF sent\n")

		if err := h.readData(lreader); err != nil {
			log.Printf("Error reading data from stream: %v\n", err)
		}
		//log.Println("sendToNextPeer  - Closing")
		timer.Stop()
		once.Do(func() {
			s.Close()
		})
	}
}
