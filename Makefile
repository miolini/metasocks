GO=go
TARGET=metasocks

all: build

build:
	$(GO) build -o $(TARGET) metasocks.go

clean:
	rm -f $(TARGET)