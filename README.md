# guac

`guac` reads and types into an Apache Guacamole SSH session that is open in
Chrome. It is for people and agents that want a Guacamole terminal on the
command line. It does not log in or open connections; you do that in Chrome.

## Install

    go install github.com/djosh34/guacamole/cmd/guac@latest

## Connect to Chrome

Start Chrome with remote debugging. Chrome allows it only with its own
profile directory:

    # macOS
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
      --remote-debugging-port=9222 --user-data-dir="$HOME/.guac-chrome"

    # Linux
    google-chrome --remote-debugging-port=9222 --user-data-dir="$HOME/.guac-chrome"

Log in to Guacamole in that Chrome and open the SSH connection. Then:

    export BROWSER_CDP_URL=http://127.0.0.1:9222

`--endpoint URL` does the same for one command.

## Commands

    guac sessions                  # list the Guacamole tabs
    guac screen                    # print the text on the terminal screen
    guac send kubectl get pods     # Ctrl+C, paste the command, Enter
    guac key ctrl+c                # press keys
    guac key y enter               # answer a prompt; send would Ctrl+C it
    guac scroll up                 # scroll one screen into the scrollback
    guac reconnect                 # click Reconnect after a disconnect

With several sessions open, pick one with `--page N` from `guac sessions`.
`guac help` and `guac COMMAND --help` tell the rest. Exit status is 0 on
success, 1 on failure and 2 on bad usage.

## Development

    go test ./...                      # unit tests
    go test -tags e2e ./e2e            # end to end, against the stack in teststack/
    go run ./teststack up              # start the stack and a logged-in Chromium
    go run ./teststack down            # stop it
