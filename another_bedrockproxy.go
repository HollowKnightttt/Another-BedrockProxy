package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type ProxyClient struct {
	mu           sync.Mutex
	nbtDir       string
	targetServer string
	flyActive    bool
	espActive    bool
	forceOpActive bool
	killauraDist float32
}

func main() {
	nbtDir := "bedrock_nbt_storage"
	if err := os.MkdirAll(nbtDir, 0755); err != nil {
		log.Fatalf("Failed to create NBT storage directory: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Destination: ")
	targetIP, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Failed to read input: %v", err)
	}
	targetIP = strings.TrimSpace(targetIP)

	if targetIP == "" {
		targetIP = "2b2tpe.org:19132"
		fmt.Printf("[*] No IP entered, defaulting to: %s\n", targetIP)
	}

	client := &ProxyClient{
		nbtDir:        nbtDir,
		targetServer:  targetIP,
		flyActive:     true,
		espActive:     true,
		forceOpActive: true,
		killauraDist:  4.5,
	}

	listener, err := minecraft.Listen("raknet", "0.0.0.0:19132")
	if err != nil {
		log.Fatalf("Failed to bind proxy listener: %v", err)
	}
	defer listener.Close()

	log.Printf("[+] Bedrock Proxy active on port 19132. Target configured to: %s", client.targetServer)
	log.Println("[+] Waiting for incoming connections...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[-] Accept error: %v", err)
			continue
		}
		go client.handleConnection(conn.(*minecraft.Conn))
	}
}

func (p *ProxyClient) handleConnection(src *minecraft.Conn) {
	defer src.Close()

	p.mu.Lock()
	destAddr := p.targetServer
	p.mu.Unlock()

	dst, err := minecraft.Dialer{
		ClientData:   src.ClientData(),
		IdentityData: src.IdentityData(),
	}.Dial("raknet", destAddr)
	
	if err != nil {
		log.Printf("[-] Failed to connect to upstream server %s: %v", destAddr, err)
		sendSystemMessage(src, fmt.Sprintf("§c[Proxy] Failed to connect to upstream server %s", destAddr))
		return
	}
	defer dst.Close()

	log.Printf("[+] Client connected. Bridging traffic to upstream %s", destAddr)

	go p.clientToServer(src, dst)
	p.serverToClient(dst, src)
}

func (p *ProxyClient) clientToServer(src, dst *minecraft.Conn) {
	for {
		pk, err := src.ReadPacket()
		if err != nil {
			log.Printf("[-] Disconnected from client: %v", err)
			break
		}

		switch pkt := pk.(type) {
		case *packet.PlayerAuthInput:
			p.mu.Lock()
			active := p.flyActive
			p.mu.Unlock()

			if active {
				// Insert custom flight/movement packet modifications here if desired
			}
		case *packet.CommandRequest:
			p.mu.Lock()
			forceOp := p.forceOpActive
			p.mu.Unlock()

			if forceOp {
				// Intercept or monitor command requests for evaluation/privilege elevation handling
				log.Printf("[ForceOP] Intercepted command request: %s", pkt.CommandLine)
			}

			if handleProxyCommand(p, src, pkt.CommandLine) {
				continue // Skip sending command to upstream server if handled locally
			}
		}

		if err := dst.WritePacket(pk); err != nil {
			log.Printf("[-] Failed to write packet to server: %v", err)
			break
		}
	}
}

func (p *ProxyClient) serverToClient(src, dst *minecraft.Conn) {
	for {
		pk, err := src.ReadPacket()
		if err != nil {
			log.Printf("[-] Disconnected from upstream server: %v", err)
			break
		}

		switch pkt := pk.(type) {
		case *packet.AddPlayer:
			p.mu.Lock()
			active := p.espActive
			p.mu.Unlock()

			if active {
				log.Printf("[ESP] Target Entity Spawned -> UUID: %s, Position: x=%.2f y=%.2f z=%.2f",
					pkt.UUID, pkt.Position.X(), pkt.Position.Y(), pkt.Position.Z())
			}
		case *packet.AvailableCommands:
			p.mu.Lock()
			forceOp := p.forceOpActive
			p.mu.Unlock()

			if forceOp {
				// Intercept server-side command definitions to evaluate permission levels
			}
		}

		if err := dst.WritePacket(pk); err != nil {
			log.Printf("[-] Failed to write packet to client: %v", err)
			break
		}
	}
}

func handleProxyCommand(p *ProxyClient, client *minecraft.Conn, cmdLine string) bool {
	if len(cmdLine) < 1 || cmdLine[0] != '/.' {
		return false
	}

	var cmd, arg string
	_, _ = fmt.Sscanf(cmdLine, "/%s %s", &cmd, &arg)

	switch cmd {
	case "forceop":
		p.mu.Lock()
		p.forceOpActive = !p.forceOpActive
		status := p.forceOpActive
		p.mu.Unlock()

		stateStr := "§cdisabled"
		if status {
			stateStr = "§aenabled"
		}
		sendSystemMessage(client, fmt.Sprintf("§7[Proxy] Force OP module is now %s", stateStr))
		return true

	case "nbtwrite":
		p.mu.Lock()
		flyState := p.flyActive
		p.mu.Unlock()

		if arg == "" {
			arg = fmt.Sprintf("save_%d", time.Now().Unix())
		}
		filename := filepath.Join(p.nbtDir, arg+".nbt")
		data := fmt.Sprintf("{proxy_save:true,timestamp:%d,fly_state:%t}", time.Now().Unix(), flyState)
		
		if err := os.WriteFile(filename, []byte(data), 0644); err != nil {
			sendSystemMessage(client, "§c[Proxy] Failed to write NBT file.")
			return true
		}
		
		sendSystemMessage(client, fmt.Sprintf("§a[Proxy] NBT state successfully written to %s.nbt", arg))
		return true

	case "nbtload":
		if arg == "" {
			sendSystemMessage(client, "§c[Proxy] Usage: /nbtload <filename>")
			return true
		}
		filename := filepath.Join(p.nbtDir, arg+".nbt")
		content, err := os.ReadFile(filename)
		if err != nil {
			sendSystemMessage(client, "§c[Proxy] NBT file not found.")
			return true
		}
		sendSystemMessage(client, fmt.Sprintf("§a[Proxy] Loaded NBT payload: %s", string(content)))
		return true
	}

	return false
}

func sendSystemMessage(client *minecraft.Conn, message string) {
	_ = client.WritePacket(&packet.Text{
		TextType: packet.TextTypeChat,
		Message:  message,
	})
}
