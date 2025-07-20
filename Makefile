# Makefile

BINARY_NAME = todays_lunch


.PHONY: all build install

all: build install

build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) .

install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin/..."
	sudo mv $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

