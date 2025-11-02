package main

import (
	"fmt"
	"github.com/splax/localvercel/builder/internal/service/deploy"
)

func main() {
	req := deploy.Request{ProjectType: "backend"}
	runtime := deploy.DetectRuntimeForCLI(req, "/Users/splax/Documents/code/peep/samples/node-app")
	fmt.Println("runtime:", runtime)
}
