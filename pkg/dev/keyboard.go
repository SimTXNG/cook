package dev

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// KeyboardAction represents an interactive terminal command.
type KeyboardAction int

const (
	ActionRestartBackend KeyboardAction = iota
	ActionRegenBindings
	ActionRestartFrontend
	ActionClear
	ActionQuit
)

// StartKeyboardListener reads lines from stdin and dispatches actions.
func StartKeyboardListener(actionCh chan<- KeyboardAction, stopCh <-chan struct{}) {
	scanner := bufio.NewScanner(os.Stdin)
	lineCh := make(chan string)

	go func() {
		defer close(lineCh)
		for scanner.Scan() {
			select {
			case lineCh <- scanner.Text():
			case <-stopCh:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			LogError("keyboard", "Failed to read stdin: %v", err)
		}
	}()

	for {
		select {
		case <-stopCh:
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			cmd := strings.TrimSpace(strings.ToLower(line))
			var action KeyboardAction
			switch cmd {
			case "r", "restart":
				action = ActionRestartBackend
			case "b", "bindings":
				action = ActionRegenBindings
			case "f", "frontend":
				action = ActionRestartFrontend
			case "c", "clear":
				action = ActionClear
			case "q", "quit", "exit":
				action = ActionQuit
			case "h", "help", "?":
				PrintHelp()
				continue
			default:
				continue
			}
			select {
			case actionCh <- action:
			case <-stopCh:
				return
			}
		}
	}
}

// PrintHelp displays interactive shortcut options in the terminal.
func PrintHelp() {
	fmt.Printf("\n%s[cook dev]%s Interactive Shortcuts:\n", ColorBold+ColorCyan, ColorReset)
	fmt.Printf("  %s[r]%s  Rebuild & restart backend\n", ColorBold, ColorReset)
	fmt.Printf("  %s[b]%s  Regenerate Wails TypeScript bindings\n", ColorBold, ColorReset)
	fmt.Printf("  %s[f]%s  Restart frontend dev server\n", ColorBold, ColorReset)
	fmt.Printf("  %s[c]%s  Clear terminal screen\n", ColorBold, ColorReset)
	fmt.Printf("  %s[q]%s  Quit cook dev\n\n", ColorBold, ColorReset)
}
