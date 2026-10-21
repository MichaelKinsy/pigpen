package schedule_prompt

import "crypto/rand"

// nanoid(size): the default URL-friendly alphabet, one random byte per character (nanoid 5's urlAlphabet: the byte masked to 6 bits).
const nanoidAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

func nanoid(size int) string {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, size)
	for i, v := range b {
		out[i] = nanoidAlphabet[v&63]
	}
	return string(out)
}
