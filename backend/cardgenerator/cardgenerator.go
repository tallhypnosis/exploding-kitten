package cardgenerator

import (
	"math/rand"
	"time"
)

var Characters = []string{
	"Cat card 😼",
	"Defuse card 🙅‍♂️",
	"Shuffle card 🔀 ",
	"Exploding kitten card 💣",
}

func GenerateRandomCards() []string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomDeck := make([]string, 5)
	for i := 0; i < 5; i++ {
		index := r.Intn(4)
		randomDeck[i] = Characters[index]
	}
	return randomDeck
}
