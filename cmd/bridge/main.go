package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mell/bridge/internal/daemon"
	"github.com/mell/bridge/internal/ipc"
	"github.com/mell/bridge/internal/menu"
	"github.com/mell/bridge/internal/relay"
	"github.com/mell/bridge/ui/tray"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "bridge",
		Short: "Send files between your machines over LAN or an optional relay",
		Long:  "Run without args (or `bridge menu`) for an interactive CLI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return menu.Run()
		},
	}

	root.AddCommand(
		cmdMenu(),
		cmdDaemon(),
		cmdRelay(),
		cmdTray(),
		cmdStatus(),
		cmdPeers(),
		cmdPair(),
		cmdSend(),
		cmdIncoming(),
		cmdAccept(),
		cmdReject(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func cmdMenu() *cobra.Command {
	return &cobra.Command{
		Use:   "menu",
		Short: "Interactive CLI menu (daemon / pair / send)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return menu.Run()
		},
	}
}

func cmdDaemon() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the background agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := daemon.New()
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			err = d.Run(ctx)
			if err != nil && err.Error() == "http: Server closed" {
				return nil
			}
			return err
		},
	}
}

func cmdRelay() *cobra.Command {
	var addr, token string
	c := &cobra.Command{
		Use:   "relay",
		Short: "Run an optional self-hosted relay",
		RunE: func(cmd *cobra.Command, args []string) error {
			s := relay.NewServer(token)
			return s.ListenAndServe(addr)
		},
	}
	c.Flags().StringVar(&addr, "listen", ":7330", "listen address")
	c.Flags().StringVar(&token, "token", "", "shared relay token")
	return c
}

func cmdTray() *cobra.Command {
	return &cobra.Command{
		Use:   "tray",
		Short: "Open the system tray UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tray.Run()
		},
	}
}

func cmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			s, err := c.Status()
			if err != nil {
				return err
			}
			fmt.Printf("device:  %s (%s)\n", s.DeviceName, s.DeviceID)
			fmt.Printf("fp:      %s\n", s.Fingerprint)
			fmt.Printf("inbox:   %s\n", s.InboxDir)
			fmt.Printf("listen:  %d\n", s.ListenPort)
			if s.RelayURL != "" {
				fmt.Printf("relay:   %s\n", s.RelayURL)
			} else {
				fmt.Printf("relay:   (not configured)\n")
			}
			return nil
		},
	}
}

func cmdPeers() *cobra.Command {
	return &cobra.Command{
		Use:   "peers",
		Short: "List paired peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			peers, err := c.Peers()
			if err != nil {
				return err
			}
			if len(peers) == 0 {
				fmt.Println("no paired peers")
				return nil
			}
			for _, p := range peers {
				state := "offline"
				if p.Online {
					state = "online"
					if p.Host != "" {
						state += " @" + p.Host
					}
				}
				fmt.Printf("%-20s %s  %s\n", p.Name, p.DeviceID[:8]+"…", state)
			}
			return nil
		},
	}
}

func cmdPair() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "pair [PIN]",
		Short: "Start pairing (no args) or join with a PIN",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ipc.New()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				s, err := client.PairStart()
				if err != nil {
					return err
				}
				fmt.Printf("Pairing PIN: %s\n", s.PIN)
				fmt.Printf("Expires:     %s\n", s.ExpiresAt)
				fmt.Println("On the other device run: bridge pair " + s.PIN)
				return nil
			}
			if err := client.PairJoin(args[0], addr); err != nil {
				return err
			}
			fmt.Println("paired successfully")
			return nil
		},
	}
	c.Flags().StringVar(&addr, "addr", "", "host:port of the device showing the PIN (skips mDNS)")
	return c
}

func cmdSend() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "send <peer> <path>",
		Short: "Send a file or folder to a paired peer",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			res, err := c.Send(args[0], args[1], yes)
			if err != nil {
				return err
			}
			if res.Status == "rejected" {
				return fmt.Errorf("rejected: %s", res.Error)
			}
			if res.Status == "failed" {
				return fmt.Errorf("failed: %s", res.Error)
			}
			fmt.Printf("status: %s (%s)\n", res.Status, res.TransferID)
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "reserved for non-interactive accept on sender side")
	return c
}

func cmdIncoming() *cobra.Command {
	return &cobra.Command{
		Use:   "incoming",
		Short: "List pending incoming transfers",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			list, err := c.Incoming()
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("no incoming transfers")
				return nil
			}
			for _, in := range list {
				fmt.Printf("%s  %s  %d bytes  from %s\n", in.TransferID, in.Name, in.Size, in.FromName)
			}
			return nil
		},
	}
}

func cmdAccept() *cobra.Command {
	return &cobra.Command{
		Use:   "accept <transfer_id>",
		Short: "Accept an incoming transfer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			return c.Accept(args[0])
		},
	}
}

func cmdReject() *cobra.Command {
	return &cobra.Command{
		Use:   "reject <transfer_id>",
		Short: "Reject an incoming transfer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := ipc.New()
			if err != nil {
				return err
			}
			return c.Reject(args[0], "rejected by user")
		},
	}
}
