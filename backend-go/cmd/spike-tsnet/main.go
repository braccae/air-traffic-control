package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"tailscale.com/tsnet"
)

func main() {
	targetHost := flag.String("host", "", "Target Tailscale hostname or IP (e.g. 100.x.y.z or magicdns name)")
	targetPort := flag.Int("port", 22, "Target SSH port")
	user := flag.String("user", "root", "SSH user to authenticate as")
	keyPath := flag.String("key", "", "Path to private key file for fallback auth (optional)")
	useTsnet := flag.Bool("tsnet", false, "Use embedded tsnet server (requires TS_AUTHKEY or existing state)")
	hostname := flag.String("ts-hostname", "atc-spike-tester", "tsnet hostname to advertise")
	dir := flag.String("ts-dir", "/tmp/atc-spike-tsnet", "Directory for tsnet state")
	flag.Parse()

	if *targetHost == "" {
		fmt.Println("ATC Tailscale tsnet & Pure-Go SSH Spike Runner")
		fmt.Println("Usage: go run ./cmd/spike-tsnet -host <100.x.y.z> [-tsnet] [-user <user>] [-key <keypath>]")
		fmt.Println("Running local loopback self-test mode...")
		runLoopbackSelfTest()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var conn net.Conn
	var err error

	if *useTsnet {
		log.Printf("Initializing tsnet server in %s (hostname: %s)...", *dir, *hostname)
		s := &tsnet.Server{
			Dir:       *dir,
			Hostname:  *hostname,
			Ephemeral: true,
		}
		defer s.Close()

		targetAddr := net.JoinHostPort(*targetHost, fmt.Sprintf("%d", *targetPort))
		log.Printf("Dialing %s via tsnet.Server.Dial...", targetAddr)
		conn, err = s.Dial(ctx, "tcp", targetAddr)
		if err != nil {
			log.Fatalf("tsnet.Server.Dial failed: %v", err)
		}
	} else {
		targetAddr := net.JoinHostPort(*targetHost, fmt.Sprintf("%d", *targetPort))
		log.Printf("Dialing %s directly over standard network...", targetAddr)
		var d net.Dialer
		conn, err = d.DialContext(ctx, "tcp", targetAddr)
		if err != nil {
			log.Fatalf("Standard dial failed: %v", err)
		}
	}
	defer conn.Close()

	log.Printf("Connection established. Testing pure-Go SSH handshake...")
	var authMethods []ssh.AuthMethod
	if *keyPath != "" {
		keyBytes, err := os.ReadFile(*keyPath)
		if err != nil {
			log.Fatalf("Failed to read private key: %v", err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			log.Fatalf("Failed to parse private key: %v", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		log.Printf("Loaded fallback public key from %s", *keyPath)
	}

	// Tailscale SSH allows none auth when client is verified by Tailscale node identity.
	// golang.org/x/crypto/ssh automatically tests none-auth first during handshake.
	clientConfig := &ssh.ClientConfig{
		User:            *user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(*targetHost, fmt.Sprintf("%d", *targetPort)), clientConfig)
	if err != nil {
		log.Fatalf("SSH handshake failed: %v (none-auth was rejected and no valid fallback auth succeeded)", err)
	}
	defer sshConn.Close()

	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("Failed to create SSH session: %v", err)
	}
	defer session.Close()

	out, err := session.Output("uname -a")
	if err != nil {
		log.Fatalf("Failed to run 'uname -a': %v", err)
	}

	log.Printf("SUCCESS! Remote output: %s", string(out))
}

func runLoopbackSelfTest() {
	log.Println("Starting mock in-memory SSH server with NoClientAuth (simulating Tailscale SSH none-auth)...")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate RSA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		log.Fatalf("Failed to create signer: %v", err)
	}

	srvConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	srvConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to bind listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(conn, srvConfig)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					channel, reqs, err := ch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range reqs {
							if r.Type == "exec" {
								r.Reply(true, nil)
								channel.Write([]byte("Linux mock-node 6.8.0 #1 SMP PREEMPT\n"))
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								channel.Close()
								return
							}
							r.Reply(false, nil)
						}
					}()
				}
			}(c)
		}
	}()

	addr := listener.Addr().String()
	log.Printf("Mock SSH server listening on %s", addr)

	log.Println("Connecting client with 0 keys (pure none-auth)...")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to dial mock: %v", err)
	}
	defer conn.Close()

	clientConfig := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		log.Fatalf("Handshake with none-auth failed: %v", err)
	}
	defer c.Close()

	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput("uname -a")
	if err != nil {
		log.Fatalf("Command execution failed: %v", err)
	}
	log.Printf("Loopback test successful! Received: %s", string(out))
}
