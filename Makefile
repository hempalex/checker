BINARY=checker
PLATFORMS=darwin linux freebsd
ARCHITECTURES=amd64 arm64

all: build

build:
	go mod tidy
	go build -o bin/checker src/checker.go src/csvfile.go
	go build -o bin/csv2xlsx src/csv2xlsx.go

run:
	go run src/checker.go csvfile.go

release:
	$(foreach GOOS, $(PLATFORMS), \
		$(foreach GOARCH, $(ARCHITECTURES), \
			$(shell \
				export GOOS=$(GOOS); \
				export GOARCH=$(GOARCH); \
				go build -o release/$(BINARY)-$(GOOS)-$(GOARCH) src/checker.go src/csvfile.go \
			) \
		) \
	)

clean:
	rm -rf bin release

