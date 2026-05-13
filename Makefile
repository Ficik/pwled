BINARY  := pwled
CMD     := ./cmd/$(BINARY)
BINDIR  := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build cross clean tidy run

all: build

build:
	go build $(LDFLAGS) -o $(BINDIR)/$(BINARY) $(CMD)

# Pi 4 / Pi 5 running a 64-bit OS
build-arm64:
	GOOS=linux GOARCH=arm64 \
		go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)-arm64 $(CMD)

# Pi 2 / Pi 3 running 32-bit OS (armhf = ARMv7 hard-float)
build-armhf:
	GOOS=linux GOARCH=arm GOARM=7 \
		go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)-armhf $(CMD)

# Pi Zero / Pi 1 (ARMv6, in case you ever need it)
build-armv6:
	GOOS=linux GOARCH=arm GOARM=6 \
		go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)-armv6 $(CMD)

cross: build-arm64 build-armhf

tidy:
	go mod tidy

run: build
	$(BINDIR)/$(BINARY) $(ARGS)

clean:
	rm -f $(BINDIR)/$(BINARY) $(BINDIR)/$(BINARY)-*
