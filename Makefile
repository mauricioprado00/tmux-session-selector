BINARY  := tmux-session-selector
GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SOURCES := $(shell find cmd internal -name '*.go' -not -name '*_test.go') go.mod

# Run as root (sudo make install), install for every user; otherwise into the
# user's own ~/.local/bin, which needs no privileges.
ifeq ($(shell id -u),0)
BINDIR ?= /usr/local/bin
else
BINDIR ?= $(HOME)/.local/bin
endif

.PHONY: build install uninstall test clean

build: $(BINARY)

$(BINARY): $(SOURCES)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $@ ./cmd/$(BINARY)

install: $(BINARY)
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	@echo "installed $(DESTDIR)$(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: $(BINDIR) is not on your PATH" ;; esac

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

test:
	$(GO) test ./...

clean:
	rm -f $(BINARY)
