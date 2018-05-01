BUILD_DIR=./build
SOURCE_DIR=../../../cmd/kplr/
IMAGE_TAG=kplr/kplr
BUILD_PREFIX=GOOS=linux GOARCH=amd64

.PHONE: all
.PHONY: build
.PHONY: clean
.PHONY: image

all: clean build

build:
	@echo "\n===> Build"
	mkdir -p $(BUILD_DIR)
	$(BUILD_PREFIX) go build -o $(BUILD_DIR)/kplr $(SOURCE_DIR)

clean:
	@echo "\n===> Clean"
	rm -rf $(BUILD_DIR)

image: all
	@echo "\n===> Image"
	docker build -t $(IMAGE_TAG) .


