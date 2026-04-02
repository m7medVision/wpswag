default:
    @just --list

build:
    go build -o wpswag .

ci: build
