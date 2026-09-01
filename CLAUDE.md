## Backend
- Go 1.26
- go-fiber 3

Placement of backend files in `internal` directory.

## Frontend
- Fyne v2.8 compiled to WebAssembly (GOOS=js GOARCH=wasm)
- Own Go module in `web-ui`, built with `go tool fyne package -os wasm`

Placement of frontend files in `web-ui` directory: `main.go` is the entry
point and everything else lives in the `web-ui/internal/ui` package, which
exports only `New`, `NewDarkTheme` and the `UI` type's `Build`/`Start`. Wire
types shared with the backend live in `web-ui/api` and are pulled into the
root module via a `replace` directive - add new request/response structs
there, never twice.

Build the bundle with `make web-ui`; it lands in `web-ui/wasm`, which the
`web-ui/wasm` package embeds and the server imports.

# Running the project, and check docker build

In the project root directory, run command:
```sh
  make run
```

# Linting the project (after changes backend code)
In the project root directory, run command:
```sh
  make vet
```

# Building the project
In the project root directory, run command:
```sh
  make build
```

# Browser tests
The frontend is one WebAssembly canvas, so the browser tests in `e2e` click by
coordinate and assert against the REST API. They need a running instance -
see `e2e/README.md` - and are run with:
```sh
  make e2e
```

For manual testing possible to use `playwright-cli`
