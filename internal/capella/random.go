package capella

import (
	"math/rand"
	"time"
)

var (
	nameLeft = []string{
		"admiring", "adoring", "amazing", "brave", "charming", "clever", "cool",
		"determined", "eager", "elegant", "epic", "friendly", "funny", "gifted",
		"happy", "hopeful", "inspiring", "jolly", "keen", "lucid", "magical",
		"modest", "nice", "peaceful", "quirky", "relaxed", "sharp", "silly",
		"stoic", "sweet", "tender", "upbeat", "vibrant", "wonderful", "zealous", "zen",
	}
	nameRight = []string{
		"archimedes", "babbage", "bohr", "curie", "darwin", "einstein", "euclid",
		"faraday", "feynman", "galileo", "hamilton", "hopper", "kepler", "lovelace",
		"maxwell", "newton", "pascal", "tesla", "turing", "wright",
	}
	rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// RandomName returns a docker-style random name.
func RandomName() string {
	for {
		name := nameLeft[rnd.Intn(len(nameLeft))] + "-" + nameRight[rnd.Intn(len(nameRight))]
		if name != "boring-wozniak" {
			return name
		}
	}
}
