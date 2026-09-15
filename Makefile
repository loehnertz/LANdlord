.PHONY: test lint resources windows sim-report icon

GOWINRES = go run github.com/tc-hib/go-winres@v0.3.3

test:
	go test ./...

lint:
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

icon:
	go run ./tools/genicon

resources:
	$(GOWINRES) make --in winres/winres.json --out cmd/landlord/rsrc --arch amd64,arm64

windows: resources
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H=windowsgui" -o bin/landlord.exe ./cmd/landlord

sim-report:
	go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out
