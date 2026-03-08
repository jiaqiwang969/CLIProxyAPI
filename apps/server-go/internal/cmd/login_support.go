package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// LoginOptions contains the shared interactive options used by supported
// provider login flows.
type LoginOptions struct {
	NoBrowser    bool
	CallbackPort int
	Prompt       func(prompt string) (string, error)
}

func defaultProjectPrompt() func(string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	return func(prompt string) (string, error) {
		fmt.Print(prompt)
		line, errRead := reader.ReadString('\n')
		if errRead != nil {
			if errors.Is(errRead, io.EOF) {
				return strings.TrimSpace(line), nil
			}
			return "", errRead
		}
		return strings.TrimSpace(line), nil
	}
}
