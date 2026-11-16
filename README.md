# guacamole

Work in progress: a CLI for Apache Guacamole SSH sessions open in Chrome.

## Development

    go test ./...                      # unit tests
    go test -tags e2e ./e2e            # end to end, against the stack in teststack/
    go run ./teststack up              # start the stack and a logged-in Chromium
    go run ./teststack down            # stop it
