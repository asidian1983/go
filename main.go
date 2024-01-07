package main

import (
	"fmt"
	"strings"
)

func multiply(a, b int) int {
	return a * b
}

func lenAndUpper(name string) (int, string) {
	return len(name), strings.ToUpper(name)
}

func main() {
	const name string = "test"
	test := false
	var male bool = false

	fmt.Println(test, male)
	fmt.Println(multiply(2, 2))

	totalLengtht, upperText := lenAndUpper(name)
	fmt.Println(totalLengtht, upperText)
}
