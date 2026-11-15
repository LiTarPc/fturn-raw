package manual

// Optional offline Android host test. It never accesses a provider or changes VPN.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/captchaipc"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk/internal/captcha"
)

func TestBrowserPowWebViewFixture(t *testing.T) {
	if os.Getenv("FREETURN_RAW_WEBVIEW_FIXTURE") != "1" {
		t.Skip("requires explicit offline WebView host fixture opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	broker, err := captchaipc.FromEnvironment(ctx)
	if err != nil {
		t.Fatal("fixture IPC unavailable")
	}
	if broker == nil || broker.Protocol() != 2 {
		t.Fatal("fixture requires authenticated IPC v2")
	}
	defer broker.Close()
	path, err := newPowCapturePath()
	if err != nil {
		t.Fatal("fixture entropy unavailable")
	}
	key := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/local-captcha-pow/", powCaptureHandler(ctx, path, key))
	html := `<!doctype html><html><head>` + injectScriptTag("http://localhost:8765", "http://localhost:8765", path) + `<script>
 (async()=>{const input='offline-webview-fixture-20261107';let nonce=0,hash='';
 do{const data=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(input+nonce));hash=Array.from(new Uint8Array(data),v=>v.toString(16).padStart(2,'0')).join('');if(hash.startsWith('0'))break;nonce++;}while(nonce<100000);
 window.captchaPowResult='v2.'+btoa(JSON.stringify({hash,nonce,duration_ms:1,telemetry:{ua:{ok:true,result:{userAgent:navigator.userAgent}}}}));
 })();</script></head><body>Offline browser proof fixture</body></html>`
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, html)
	})
	var id string
	proof, err := runCaptchaServerAndWait(context.WithValue(ctx, quietPresenterKey{}, true), mux, "http://localhost:8765/fixture", key, nil, "offline fixture", func(raw string) {
		id, err = broker.BeginCapture(ctx, raw, cancel)
		if err != nil {
			cancel()
		}
	})
	if err != nil {
		if id != "" {
			broker.End(id, "unavailable")
		}
		t.Fatal("offline browser fixture did not capture within TTL")
	}
	broker.End(id, "captured")
	var value struct {
		Hash      string          `json:"hash"`
		Nonce     int             `json:"nonce"`
		Telemetry json.RawMessage `json:"telemetry"`
	}
	if json.Unmarshal([]byte(captcha.DecodePowEnvelope(proof)), &value) != nil || value.Nonce < 0 {
		t.Fatal("invalid fixture envelope")
	}
	sum := sha256.Sum256([]byte("offline-webview-fixture-20261107" + strconv.Itoa(value.Nonce)))
	if hex.EncodeToString(sum[:]) != value.Hash || !strings.HasPrefix(value.Hash, "0") || len(value.Telemetry) == 0 {
		t.Fatal("wrong fixture proof")
	}
	fmt.Fprintln(os.Stdout, "FTURN_FIXTURE browser_pow=validated authorization=not_assessed")
}
