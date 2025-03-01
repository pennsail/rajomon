package rajomon

import (
	"fmt"
	"time"
)

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	if debug {
		timestamp := time.Now().Format("2006-01-02T15:04:05.999999999-07:00")
		fmt.Printf("LOG: "+timestamp+"|\t"+format+"\n", a...)
	}
}

func recordPrice(format string, a ...interface{}) {
	if trackPrice {
		timestamp := time.Now().Format("2006-01-02T15:04:05.999999999-07:00")
		fmt.Printf("LOG: "+timestamp+"|\t"+format+"\n", a...)
	}
}
