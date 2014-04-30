// @author Robin Verlangen
// Discovery service used to detect cluster

package main

// Imports
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Discovery constants
const PING_TIMEOUT = 30 * time.Second
const PING_INTERVAL = 1 * time.Second

// Node (entity in the Dispenso cluster)
type Node struct {
	DiscoveryService *DiscoveryService // Discovery service reference
	Host             string            // Fully qualified hostname
	Port             int               // Port on which Dispenso runs

	metaReceived bool         // Did we receive metadata?
	mux          sync.RWMutex // Locking mechanism
}

// Full name
func (n *Node) FullName() string {
	return fmt.Sprintf("%s:%d", n.Host, n.Port)
}

// Full url
func (n *Node) FullUrl(service string) string {
	return fmt.Sprintf("http://%s/%s", n.FullName(), service)
}

// Fetch node metadata
func (n *Node) FetchMeta() bool {
	resp, err := http.Get(n.FullUrl("discovery"))
	if err != nil {
		log.Println(fmt.Sprintf("ERR: Failed to fetch node metadata %s"), err)
		return false
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(fmt.Sprintf("ERR: Failed to read node metadata %s"), err)
		return false
	}

	// Parse json
	var f interface{}
	err = json.Unmarshal(body, &f)
	if err != nil {
		log.Println(fmt.Sprintf("ERR: Failed to parse node metadata %s"), err)
		return false
	}
	m := f.(map[string]interface{})
	log.Println(fmt.Sprintf("DEBUG: %s", m["time"]))

	// Meta received
	n.mux.Lock()
	n.metaReceived = true
	log.Println(fmt.Sprintf("INFO: Detected %s", n.FullName()))
	n.mux.Unlock()

	// Exchange meta
	n.ExchangeMeta()

	return true
}

// Exchange node metadata
func (n *Node) ExchangeMeta() bool {
	// Client
	httpclient := &http.Client{}

	log.Println("INFO: Exchanging metadata")

	// Metadata
	var data map[string]string = make(map[string]string)
	var nodeStrs []string = make([]string, len(n.DiscoveryService.Nodes))
	for _, node := range n.DiscoveryService.Nodes {
		nodeStrs = append(nodeStrs, fmt.Sprintf("%s:%d", node.Host, node.Port))
	}
	data["nodes"] = strings.Join(nodeStrs, ",")

	// To JSON
	b, err := json.Marshal(data)
	if err != nil {
		log.Println(fmt.Sprintf("ERR: Failed to format json"))
		return false
	}

	// Execute request
	req, reqErr := http.NewRequest("POST", n.FullUrl("discovery"), bytes.NewBufferString(fmt.Sprintf("%s", b)))
	req.Header.Set("User-Agent", "FlxOne Real-Time Event Hook")
	if reqErr != nil {
		log.Println(fmt.Sprintf("ERR: Failed request: %s", reqErr))
		return false
	}

	// Parse response
	resp, respErr := httpclient.Do(req)
	if respErr != nil {
		log.Println(fmt.Sprintf("ERR: Failed request: %s", respErr))
		return false
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// Read response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(fmt.Sprintf("ERR: Failed to read node metadata exchange response %s"), err)
		return false
	}
	log.Println(fmt.Sprintf("%s", body))
	return true
}

// Ping a node
func (n *Node) Ping() bool {
	// Knock on the door
	conn, err := net.DialTimeout("tcp", n.FullName(), PING_TIMEOUT)
	if err != nil {
		return false
	}
	conn.Close()

	// Try to fetch metadata
	n.mux.RLock()
	if n.metaReceived == false {
		go func() {
			n.FetchMeta()
		}()
	}
	n.mux.RUnlock()

	// OK
	return true
}

// Message (payload transmitted between nodes containing instructions)
type Message struct {
	Type    MessageType // Type of message
	Payload string      // JSON payload
}

// Message types, enum-like datastructure, use "MessageType" as wrapper
type MessageType struct {
	code messageType
}
type messageType int

const (
	discoveryPing     messageType = iota + 1 // Initial discovery ping
	disocveryResponse                        // Discovery response
	discoveryMeta                            // Metadata beyond initial discovery
	configuration                            // Used to update configuration in the cluster
	taskRequest                              // New task submission
	taskApproval                             // Approve task
	taskReject                               // Reject task
	taskExecution                            // After being approved a task execution will be sent to the nodes
)

// Discovery service
type DiscoveryService struct {
	Nodes []*Node // List of nodes
}

// Create discovery service
func NewDiscoveryService() *DiscoveryService {
	return &DiscoveryService{}
}

// Set seeds
func (d *DiscoveryService) SetSeeds(seeds []string) error {
	for _, seed := range seeds {
		// Simple seed validation
		split := strings.Split(seed, ":")
		var port int = defaultPort
		if len(split) > 2 {
			log.Println(fmt.Sprintf("ERROR: Seed %s host:port format invalid", seed))
			continue
		} else if len(split) == 1 {
			// Default port
		} else {
			// User port
			var err error
			port, err = strconv.Atoi(split[1])
			if err != nil {
				log.Println(fmt.Sprintf("ERROR: Seed %s port format invalid", seed))
				continue
			}
		}

		// Add node
		n := &Node{
			DiscoveryService: d,
			Host:             split[0],
			Port:             port,
		}
		d.Nodes = append(d.Nodes, n)
	}
	return nil
}

// Run discovery service
func (d *DiscoveryService) Start() bool {
	go func() {
		log.Println("INFO: Starting discovery")

		// Iterate nodes
		ticker := time.NewTicker(PING_INTERVAL)
		for {
			select {
			case <-ticker.C:
				// Discover nodes
				for _, node := range d.Nodes {
					if !node.Ping() {
						log.Println(fmt.Sprintf("WARN: Failed to detect %s", node.FullName()))
					}
				}
			case <-shutdown:
				ticker.Stop()
				return
			}
		}

		// @todo Run every once in a while, and remove shutdown

		//shutdown <- true
	}()
	return true
}