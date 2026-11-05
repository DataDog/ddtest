package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func checkMiniInheritedOwnerCache(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"owner_test.go": `package fixture_test
import("os";"testing")
func sharedOwnerRow(t *testing.T){f,err:=os.OpenFile(os.Getenv("OWNER_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};f.WriteString("r");f.Close();if _,err:=os.Stat(os.Getenv("OWNER_SECOND"));err==nil{t.Error("second declaration row fails")}}
//ddtest:no_auto_retry
func TestFirstOwner(t *testing.T){t.Run("row",sharedOwnerRow)}
//ddtest:no_auto_retry
func TestSecondOwner(t *testing.T){t.Run("row",sharedOwnerRow)}
func TestOwnerSwitch(t *testing.T){f,err:=os.OpenFile(os.Getenv("OWNER_SECOND"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.Close();os.Remove(os.Getenv("OWNER_SECOND"));t.Run("case",TestFirstOwner);os.WriteFile(os.Getenv("OWNER_SECOND"),[]byte("second"),0600);t.Error("retry parent");return};if !os.IsExist(err){t.Fatal(err)};t.Run("case",TestSecondOwner)}
`})
	bin := filepath.Join(t.TempDir(), executableName("owner.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			_, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestOwnerSwitch$"}, Policy: policySettings{Retry: true}, Env: []string{"OWNER_CALLS=" + calls, "OWNER_SECOND=" + filepath.Join(t.TempDir(), "second"), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
			data, err := os.ReadFile(calls)
			if got.code != 1 || err != nil || string(data) != "rr" {
				t.Fatalf("inherited declaration collision calls=%q err=%v exit=%d %s %s", data, err, got.code, got.out, got.stderr)
			}
		})
	}
}
