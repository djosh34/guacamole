// Command teststack starts and stops the Guacamole test stack of this
// checkout, with a headless Chromium that is logged in to it.
//
//	go run ./teststack up          start the stack and open the default connection
//	go run ./teststack open NAME   open another connection in a new tab
//	go run ./teststack down        stop it all and remove the containers
//
// up and open print the URLs to use, as shell variable assignments.
// $GUAC_VERSION picks the Guacamole version (default 1.6.0).
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
	switch {
	case len(args) == 1 && args[0] == "up":
		return open(ctx, root, "default")
	case len(args) == 2 && args[0] == "open":
		return open(ctx, root, args[1])
	case len(args) == 1 && args[0] == "down":
		return teststack.Down(ctx, root)
	}
	return fmt.Errorf("usage: go run ./teststack up | open NAME | down")
}

func open(ctx context.Context, root, connection string) error {
	s, err := teststack.Up(ctx, root)
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
