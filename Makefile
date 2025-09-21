SHELL := /bin/bash

.PHONY: *

mocks:
    find . -type f -name 'mock_*.go' -delete
    mockery
