// Command teststack starts and stops the Guacamole test stack of this
// checkout, with a headless Chromium that is logged in to it.
//
//	go run ./teststack up [VERSION]   start the stack and open the default connection
//	go run ./teststack open NAME      open another connection in a new tab
//	go run ./teststack down           stop it all and remove the containers
//
// up and open print the URLs to use, as shell variable assignments.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/djosh34/guacamole/internal/teststack"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "teststack: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := teststack.Root(wd)
	if err != nil {
		return err
	}
	cmd, arg := "", ""
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 {
		arg = args[1]
	}
	switch {
	case cmd == "up" && len(args) <= 2:
		return open(ctx, root, arg, "default")
	case cmd == "open" && len(args) == 2:
		return open(ctx, root, "", arg)
	case cmd == "down" && len(args) == 1:
		return teststack.Down(ctx, root)
	}
	return fmt.Errorf("usage: go run ./teststack up [VERSION] | open NAME | down")
}

func open(ctx context.Context, root, version, connection string) error {
	s, err := teststack.Up(ctx, root, version)
	if err != nil {
		return err
	}
	tab, err := s.Open(ctx, connection, teststack.View{})
	if err != nil {
		return err
	}
	// Closing the connection leaves the tab open.
	if err := tab.Conn.Close(); err != nil {
		return err
	}
	fmt.Printf("GUAC_URL=%s\nBROWSER_CDP_URL=%s\n", s.GuacURL, s.CDPURL)
	return nil
}
