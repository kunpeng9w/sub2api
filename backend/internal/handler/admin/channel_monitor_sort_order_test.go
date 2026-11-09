//go:build unit

package admin

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorSortOrderBinding(t *testing.T) {
	for _, value := range []string{"0", "10", "2147483647", "-1", "1.5", "2147483648", `"10"`} {
		t.Run(value, func(t *testing.T) {
			valid := value == "0" || value == "10" || value == "2147483647"
			for _, update := range []bool{false, true} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"name":"test","provider":"openai","sort_order":%s}`, value)))
				c.Request.Header.Set("Content-Type", "application/json")
				var err error
				if update {
					var req channelMonitorUpdateRequest
					err = c.ShouldBindJSON(&req)
				} else {
					var req channelMonitorCreateRequest
					err = c.ShouldBindJSON(&req)
				}
				if valid {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}
