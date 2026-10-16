package menu

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mell/bridge/internal/daemon"
	"github.com/mell/bridge/internal/ipc"
)

// Run starts an interactive CLI menu. If the daemon is not up, option 1
// starts it in-process so pair/send work from the same terminal.
func Run() error {
	in := bufio.NewReader(os.Stdin)
	var (
		daemonCancel context.CancelFunc
		daemonErr    = make(chan error, 1)
	)
	defer func() {
		if daemonCancel != nil {
			daemonCancel()
		}
	}()

	for {
		fmt.Println()
		fmt.Println("=== bridge ===")
		printDaemonHint()
		fmt.Println("1) Start daemon")
		fmt.Println("2) Show pairing PIN (this device)")
		fmt.Println("3) Enter pairing PIN (other device)")
		fmt.Println("4) List peers")
		fmt.Println("5) Send file/folder")
		fmt.Println("6) Incoming transfers")
		fmt.Println("7) Status")
		fmt.Println("0) Quit")
		fmt.Print("> ")

		choice, err := readLine(in)
		if err != nil {
			return err
		}
		switch strings.TrimSpace(choice) {
		case "1":
			if daemonCancel != nil {
				fmt.Println("daemon already started in this menu")
				continue
			}
			if clientOK() {
				fmt.Println("daemon already running")
				continue
			}
			d, err := daemon.New()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			daemonCancel = cancel
			go func() {
				err := d.Run(ctx)
				if err != nil && err.Error() != "http: Server closed" {
					daemonErr <- err
				}
			}()
			if waitDaemon(3 * time.Second) {
				fmt.Println("daemon started")
			} else {
				fmt.Println("daemon starting… if status fails, check the port")
			}
		case "2":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			c, _ := ipc.New()
			s, err := c.PairStart()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			fmt.Println()
			fmt.Println("  Pairing PIN:", s.PIN)
			fmt.Println("  Expires:    ", s.ExpiresAt)
			fmt.Println("  On the other device: menu → 3, or: bridge pair", s.PIN)
		case "3":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			fmt.Print("PIN: ")
			pin, err := readLine(in)
			if err != nil {
				return err
			}
			pin = strings.TrimSpace(pin)
			fmt.Print("Addr (host:port, empty = mDNS): ")
			addr, err := readLine(in)
			if err != nil {
				return err
			}
			addr = strings.TrimSpace(addr)
			c, _ := ipc.New()
			if err := c.PairJoin(pin, addr); err != nil {
				fmt.Println("error:", err)
				continue
			}
			fmt.Println("paired successfully")
		case "4":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			c, _ := ipc.New()
			peers, err := c.Peers()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			if len(peers) == 0 {
				fmt.Println("no paired peers")
				continue
			}
			for i, p := range peers {
				state := "offline"
				if p.Online {
					state = "online"
					if p.Host != "" {
						state += " @" + p.Host
					}
				}
				fmt.Printf("  %d) %s  %s\n", i+1, p.Name, state)
			}
		case "5":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			c, _ := ipc.New()
			peers, err := c.Peers()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			if len(peers) == 0 {
				fmt.Println("no paired peers — pair first")
				continue
			}
			fmt.Println("Peers:")
			for i, p := range peers {
				state := "offline"
				if p.Online {
					state = "online"
				}
				fmt.Printf("  %d) %s (%s)\n", i+1, p.Name, state)
			}
			fmt.Print("Peer number or name: ")
			sel, err := readLine(in)
			if err != nil {
				return err
			}
			sel = strings.TrimSpace(sel)
			peerName := sel
			if n, err := strconv.Atoi(sel); err == nil && n >= 1 && n <= len(peers) {
				peerName = peers[n-1].Name
			}
			fmt.Print("Path to file/folder: ")
			path, err := readLine(in)
			if err != nil {
				return err
			}
			path = expandHome(strings.TrimSpace(path))
			fmt.Println("sending…")
			res, err := c.Send(peerName, path, true)
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			if res.Status == "rejected" || res.Status == "failed" {
				fmt.Printf("status: %s (%s)\n", res.Status, res.Error)
				continue
			}
			fmt.Printf("status: %s\n", res.Status)
		case "6":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			c, _ := ipc.New()
			list, err := c.Incoming()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			if len(list) == 0 {
				fmt.Println("no incoming transfers")
				continue
			}
			for i, in := range list {
				fmt.Printf("  %d) %s  %d bytes  from %s  id=%s\n", i+1, in.Name, in.Size, in.FromName, in.TransferID)
			}
			fmt.Print("Accept number (or empty to skip): ")
			sel, err := readLine(in)
			if err != nil {
				return err
			}
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			n, err := strconv.Atoi(sel)
			if err != nil || n < 1 || n > len(list) {
				fmt.Println("invalid choice")
				continue
			}
			if err := c.Accept(list[n-1].TransferID); err != nil {
				fmt.Println("error:", err)
				continue
			}
			fmt.Println("accepting…")
		case "7":
			if err := ensureClient(); err != nil {
				fmt.Println(err)
				continue
			}
			c, _ := ipc.New()
			s, err := c.Status()
			if err != nil {
				fmt.Println("error:", err)
				continue
			}
			fmt.Printf("  device: %s (%s)\n", s.DeviceName, s.DeviceID)
			fmt.Printf("  inbox:  %s\n", s.InboxDir)
			fmt.Printf("  listen: %d\n", s.ListenPort)
		case "0", "q", "quit", "exit":
			fmt.Println("bye")
			return nil
		default:
			fmt.Println("unknown option")
		}

		select {
		case err := <-daemonErr:
			fmt.Println("daemon error:", err)
			daemonCancel = nil
		default:
		}
	}
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil {
		return strings.TrimRight(line, "\r\n"), err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return home + path[1:]
	}
	return path
}

func clientOK() bool {
	c, err := ipc.New()
	if err != nil {
		return false
	}
	_, err = c.Status()
	return err == nil
}

func ensureClient() error {
	if clientOK() {
		return nil
	}
	return fmt.Errorf("daemon not running — choose 1) Start daemon first")
}

func waitDaemon(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if clientOK() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return clientOK()
}

func printDaemonHint() {
	if clientOK() {
		fmt.Println("(daemon: online)")
	} else {
		fmt.Println("(daemon: offline)")
	}
}
