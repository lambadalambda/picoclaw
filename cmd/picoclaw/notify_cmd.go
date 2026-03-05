package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	localnotify "github.com/sipeed/picoclaw/pkg/notify"
)

type notifyRequest struct {
	Source  string
	Content string
	Channel string
	ChatID  string
	Stdin   bool
}

func notifyCmd() {
	args := os.Args[2:]
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			notifyHelp()
			return
		}
	}

	req, err := parseNotifyRequest(args, os.Stdin)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		notifyHelp()
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	id, err := localnotify.Enqueue(cfg.WorkspacePath(), localnotify.QueueMessage{
		Source:  req.Source,
		Content: req.Content,
		Channel: req.Channel,
		ChatID:  req.ChatID,
	})
	if err != nil {
		fmt.Printf("Error queuing notification: %v\n", err)
		os.Exit(1)
	}

	targetLabel := "active chat"
	if req.Channel != "" && req.ChatID != "" {
		targetLabel = req.Channel + ":" + req.ChatID
	}
	fmt.Printf("Queued local notification %s for %s\n", id, targetLabel)
}

func parseNotifyRequest(args []string, stdin io.Reader) (notifyRequest, error) {
	req := notifyRequest{Source: "local"}
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--source":
			if i+1 >= len(args) {
				return notifyRequest{}, fmt.Errorf("--source requires a value")
			}
			req.Source = args[i+1]
			i++
		case "--stdin":
			req.Stdin = true
		case "--channel":
			if i+1 >= len(args) {
				return notifyRequest{}, fmt.Errorf("--channel requires a value")
			}
			req.Channel = args[i+1]
			i++
		case "--to", "--chat-id", "--chat":
			if i+1 >= len(args) {
				return notifyRequest{}, fmt.Errorf("%s requires a value", arg)
			}
			req.ChatID = args[i+1]
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return notifyRequest{}, fmt.Errorf("unknown option: %s", arg)
			}
			positional = append(positional, arg)
		}
	}

	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = "local"
	}
	req.Channel = strings.TrimSpace(req.Channel)
	req.ChatID = strings.TrimSpace(req.ChatID)
	if (req.Channel == "") != (req.ChatID == "") {
		return notifyRequest{}, fmt.Errorf("--channel and --to/--chat-id must be provided together")
	}

	if req.Stdin {
		if len(positional) > 0 {
			return notifyRequest{}, fmt.Errorf("do not pass message text with --stdin")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return notifyRequest{}, err
		}
		req.Content = strings.TrimSpace(string(data))
	} else {
		req.Content = strings.TrimSpace(strings.Join(positional, " "))
	}

	if req.Content == "" {
		return notifyRequest{}, fmt.Errorf("message text is required")
	}

	return req, nil
}

func notifyHelp() {
	fmt.Println("\nNotify command:")
	fmt.Println("  Queue a local message for delivery to the active chat (or an explicit target).")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  picoclaw notify [options] <message>")
	fmt.Println("  echo \"Deploy complete\" | picoclaw notify --stdin")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --source <name>      Source label (default: local)")
	fmt.Println("  --stdin              Read message text from stdin")
	fmt.Println("  --channel <name>     Explicit target channel")
	fmt.Println("  --to <chat_id>       Explicit target chat id")
}
