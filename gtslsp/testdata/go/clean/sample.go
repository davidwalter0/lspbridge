package sample

import "fmt"

// Greeter greets people.
type Greeter struct {
	Name string
}

type Speaker interface {
	Speak() string
}

const Version = "1.0"

var counter int

// Speak returns the greeting.
func (g *Greeter) Speak() string {
	return fmt.Sprintf("hello, %s", g.Name)
}

func NewGreeter(name string) *Greeter {
	counter++
	return &Greeter{Name: name}
}

func main() {
	g := NewGreeter("世界 😀")
	fmt.Println(g.Speak())
}
