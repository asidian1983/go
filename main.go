package main

import (
	"fmt"
	"strings"
)

func multiply(a, b int) int {
	return a * b
}

func superAdd(numbers ...int) int {
	total := 0
	for _, number := range numbers {
		total += number
	}

	return total
}

func lenAndUpper(name string) (lenght int, uppercase string) {
	defer fmt.Println("Done") // func 끝나고 실행 됨
	lenght = len(name)
	uppercase = strings.ToUpper(name)
	return
}

func canIDrink(age int) bool {
	if koreanAge := age + 2; koreanAge < 19 {
		return false
	} else {
		return true
	}
}

func canIDrink2(age int) bool {
	switch koreanAge := age + 2; koreanAge {
	case 10:
		return false
	case 18:
		return true
	}
	return false
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

	result := superAdd(1, 2, 3, 4, 5)
	fmt.Println(result)

	// if else
	fmt.Println(canIDrink(16))
	fmt.Println(canIDrink(18))

	// switch
	fmt.Println(canIDrink2(10))

	// pointer
	a := 2
	b := a
	c := &a
	a = 10
	*c = 20
	fmt.Println(a, b, c)
	fmt.Println(&a, &b, *c)

	// array
	names := []string{"1", "2", "3", "4", "5"}
	names[2] = "test"

	names = append(names, "append")
	fmt.Println(names)
}
