.PHONY: test lint windows sim-report

test:
	go test ./...

lint:
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H=windowsgui" -o bin/landlord.exe ./cmd/landlord

sim-report:
	go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out
