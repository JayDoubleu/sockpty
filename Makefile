.ONESHELL:

NAME=sockpty
OUTPUTDIR=$(shell pwd)/bin
INSTALLDIR=$(HOME)/.local/bin

all: build install
all-podman: build-podman install
build:
	go build -v -o $(OUTPUTDIR)/$(NAME)-server cmd/server/main.go
	go build -v -o $(OUTPUTDIR)/${NAME} cmd/client/main.go
clean: 
	rm -r $(OUTPUTDIR)/$(NAME)-server
	rm -f $(OUTPUTDIR)/${NAME}

build-podman:
	podman run --rm -it \
		--volume $(PWD):/build:Z \
		--workdir /build \
		docker.io/library/golang:latest \
		make build
install:
	install -d $(INSTALLDIR)
	install -m 700 $(OUTPUTDIR)/$(NAME)-server $(INSTALLDIR)
	install -m 700 $(OUTPUTDIR)/$(NAME) $(INSTALLDIR)
	cat <<EOF > $(HOME)/.config/systemd/user/$(NAME).service
	[Unit]
	Description=$(NAME) Service
	After=network-online.target
	Wants=network-online.target
	[Install]
	WantedBy=default.target
	[Service]
	Type=simple
	StandardOutput=journal
	ExecStart=/bin/bash -l -c $(INSTALLDIR)/$(NAME)-server
	Restart=on-failure
	KillMode=process
	EOF
	systemctl --user enable sockpty.service

uninstall:
	rm -f $(INSTALLDIR)/$(NAME)-server
	rm -f $(INSTALLDIR)/$(NAME)
	systemctl --user disable sockpty.service
	rm -f $(HOME)/.config/systemd/user/$(NAME).service
