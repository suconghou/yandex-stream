
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -o yandexstream -a -ldflags "-s -w" main.go
