# Cam Server

This project is a server that is interfaces with the Raspberry Pi camera.

The goal is to use minimal external dependencies, and demonstrate effective concurrency features within Go.

One feature of the server is to allow for graceful shutdown of all worker goroutines. This is not necessary since we don't really have anything that needs to be run before shutdown, but I thought it would be fun to add to learn more about concurrency and Go's contexts feature :). In other words, this code is more complex than necessary, and could be even more concise. Code is commented to explain the process for graceful shutdown.

## Running

- `./build.sh`
- `./cam-server`