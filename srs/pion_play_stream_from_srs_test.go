package srs

import (
	"fmt"
	"testing"
	"time"
)

func TestPionSrsPlayConnector(t *testing.T) {
	t.Run("1", func(t *testing.T) {
		c, _ := NewPionSrsPlayConnector("49.235.110.177:1985", "live", "test")
		done := c.Start()
		err := <-done
		if err != nil {
			fmt.Println(err)
			return
		}
		time.Sleep(time.Second * 20)
		c.Stop()
		time.Sleep(time.Second * 20)
	})

}
