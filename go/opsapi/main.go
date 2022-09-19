package main

import (
	"fmt"
	"opsapi/filemanager"
	"opsapi/nginx"
	"opsapi/pop"
	"opsapi/varnish"
)

func main() {

	fmt.Println("Hello, Modules OPSAPI filemanager package!")

	filemanager.PrintHello()

	nginx.PrintHello()

	varnish.PrintHello()

	pop.PrintHello()
}
