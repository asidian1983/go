package main

import (
	"fmt"
	"strings"
)

func multiply(a, b int) int {
	return a * b
}

func lenAndUpper(name string) (lenght int, uppercase string) {
	defer fmt.Println("Done") // func 끝나고 실행 됨
	lenght = len(name)
	uppercase = strings.ToUpper(name)
	return
}

func repeat(words ...string) {
	fmt.Println(words)
}

func main() {
	const name string = "test"
	test := false
	var male bool = false

	fmt.Println(test, male)
	fmt.Println(multiply(2, 2))

	totalLengtht, upperText := lenAndUpper(name)
	fmt.Println(totalLengtht, upperText)

	repeat("1", "2", "3")
}
