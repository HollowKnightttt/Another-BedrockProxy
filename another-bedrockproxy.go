package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sandertv/gophertunnel/tree/master/minecraft"
	"github.com/Sandertv/gophertunnel/tree/master/minecraft/protocol/packet"
)

type ProxyClient struct {
	mu          sync.Mutex
	nbtDir      string
	flyActive   bool
	espActive   bool
	killauraDist float32
}

func main() {
	nbtDir := "bedrock_nbt_storage"
	os.MkdirAll(nbtDir, 0755)

	client := &ProxyClient{
		nbtDir:       nbtDir,
		flyActive:    true,
		espActive:    true,
		killauraDist: 4.5,
	}

	listener, err := minecraft.Listen("raknet", "0.0.0.0:19132")
	if err != nil {
		log.Fatalf("Failed to bind proxy listener: %v", err)
	}
	defer listener.Close()

	log.Println("[+] Bedrock Proxy Hack active on port 19132. Waiting for connection...")

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

	// Connect to upstream Bedrock server (e.g., featured server or local test server)
	destAddr := "2b2tpe.org:19132"
	dst, err := minecraft.Dialer{}.Dial("raknet", destAddr)
	if err != nil {
		log.Printf("[-] Failed to connect to upstream server %s: %v", destAddr, err)
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
			break
		}

		switch pkt := pk.(type) {
		case *packet.PlayerAuthInput:
			// Inject Fly: Override movement states or enable flight flags
			if p.flyActive {
				pkt.VehicleMovement = true
				// Modify input data to bypass fall damage / movement checks
			}
		case *packet.CommandRequest:
			// Intercept custom proxy commands
			if handleProxyCommand(p, src, pkt.CommandLine) {
				continue // Skip sending command to server if handled locally
			}
		}

		if err := dst.WritePacket(pk); err != nil {
			break
		}
	}
}

func (p *ProxyClient) serverToClient(src, dst *minecraft.Conn) {
	for {
		pk, err := src.ReadPacket()
		if err != nil {
			break
		}

		switch pkt := pk.(type) {
		case *packet.AddPlayer:
			// KillAura ESP: Annotate or log spawned entities for client-side ESP rendering
			if p.espActive {
				log.Printf("[ESP] Target Entity Spawned -> ID: %v, UUID: %s, Position: x=%.2f y=%.2f z=%.2f",
					pkt.EntityUniqueID, pkt.UUID, pkt.Position.X(), pkt.Position.Y(), pkt.Position.Z())
			}
		case *packet.MobEquipment:
			// Track target equipment for combat optimization
		}

		if err := dst.WritePacket(pk); err != nil {
			break
		}
	}
}

func handleProxyCommand(p *ProxyClient, client *minecraft.Conn, cmdLine string) bool {
	if len(cmdLine) < 1 || cmdLine[0] != '/' {
		return false
	}

	var cmd, arg string
	_, _ = fmt.Sscanf(cmdLine, "/%s %s", &cmd, &arg)

	switch cmd {
	case "nbtwrite":
		if arg == "" {
			arg = fmt.Sprintf("save_%d", time.Now().Unix())
		}
		filename := filepath.Join(p.nbtDir, arg+".nbt")
		data := fmt.Sprintf("{proxy_save:true,timestamp:%d,fly_state:%t}", time.Now().Unix(), p.flyActive)
		_ = os.WriteFile(filename, []byte(data), 0644)
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
	client.WritePacket(&packet.Text{
		TextType: packet.TextTypeChat,
		Message:  message,
	})
}
