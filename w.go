package main

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/syndtr/goleveldb/leveldb"
)

//go:embed *.html
//go:embed *.txt
var contentFS embed.FS

var (
	flagListen         = flag.String("listen", ":8666", "[ip]:port to serve on")
	flagDB             = flag.String("db", "db", "Database file")
	flagIDSize         = flag.Int("id-size-min", 3, "Minimum ID size (raw bytes, before encoding)")
	flagMaxSize        = flag.Int("max-size", 32*1024*1024, "Maximum data size")
	flagExpiry         = flag.Duration("expiry", 256*24*time.Hour, "Expiry time")
	flagExpirySizeBias = flag.Int("expiry-size-bias", 128*1024, "How much to reduce expiry per day based on size (0 disables)")
	flagWasteHost      = flag.String("waste-host", "🗑️.st", "IDN host override")
	flagVersion        = flag.Bool("version", false, "Show version")
)

// crockfordAlphabet is the base32 encode alphabet as per
// https://www.crockford.com/base32.html, in lowercase.
const crockfordAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

var Base32Crockford = base32.NewEncoding(crockfordAlphabet).WithPadding(base32.NoPadding)

var db *leveldb.DB
var tpl *template.Template

type Paste struct {
	id     string
	UUID   string
	Name   string
	Text   []byte
	Syntax string
	TS     int
}

func main() {
	flag.Parse()
	tpl = template.Must(template.ParseFS(contentFS, "w.html"))

	if *flagVersion {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			log.Panic("No buildinfo available")
		}
		fmt.Fprintf(os.Stderr, "https://%v version: %v\n", info.Main.Path, info.Main.Version)
		return
	}

	var err error
	db, err = leveldb.OpenFile(*flagDB, nil)
	if err != nil {
		log.Fatal(err)
	}

	matches, _ := fs.Glob(contentFS, "*.txt")
	for _, name := range matches {
		content, _ := fs.ReadFile(contentFS, name)
		id := strings.TrimSuffix(name, filepath.Ext(name))
		name := ""
		syntax := ""
		if id == "waste.1" {
			name = "WASTE(1)"
			syntax = "ansi"
		}
		p := &Paste{
			id:   id,
			Name: name,
			Text: content,
			Syntax: syntax,
		}
		p.Save(db)
	}

	http.Handle("/", gziphandler.GzipHandler(http.HandlerFunc(errorWrap(serve))))
	http.Handle("/r/", http.HandlerFunc(errorWrap(raw)))
	go expire()
	log.Fatal(http.ListenAndServe(*flagListen, nil))
}

func expire() {
	for {
		time.Sleep(3 * time.Hour)
		log.Print("Expiry running...")

		now := time.Now().Unix()
		expireBase := int64(*flagExpiry / time.Second)

		batch := new(leveldb.Batch)
		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			var paste Paste
			if err := json.NewDecoder(bytes.NewReader(iter.Value())).Decode(&paste); err != nil {
				log.Print(err)
				continue
			}

			if paste.TS == 0 {
				continue
			}

			expiry := expireBase
			if *flagExpirySizeBias > 0 {
				size := len(paste.Text)
				if size == 0 {
					// Quickly expire empty pastes too.
					size = *flagMaxSize
				}
				expiry -= int64((24*time.Hour)/time.Second) * int64(len(paste.Text) / *flagExpirySizeBias)
			}

			if int64(paste.TS)+expiry <= now {
				batch.Delete(iter.Key())
			}
		}
		iter.Release()

		if err := db.Write(batch, nil); err != nil {
			log.Print(err)
		}
	}
}

func raw(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Path) > 3 {
		id := r.URL.Path[3:]
		paste, err := getPaste(id)
		if err != nil {
			if err == leveldb.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("Not found or expired.\n"))
				return nil
			} else {
				return err
			}
		}

		return serveRaw(paste, w, r)
	}

	http.Error(w, "Not found", http.StatusNotFound)
	return nil
}

func serveRaw(paste *Paste, w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")

	ct := http.DetectContentType(paste.Text)
	ctType, _, _ := mime.ParseMediaType(ct)
	if ctType == "text/html" {
		ct = "text/plain; charset=UTF-8"
	}

	sfd := r.Header.Get("Sec-Fetch-Dest")
	// MIME type and header must match for image and video
	if len(ct) > 5 && ct[:5] == "image" || ct[:5] == "video" {
		if sfd != "" && sfd != ct[:5] {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Location", "/"+paste.id)
			w.WriteHeader(http.StatusFound)
			return nil
		}
	}

	refUrl, err := url.Parse(r.Header.Get("Referer"))
	if paste.id != "bin" && err == nil && len(refUrl.Host) > 0 {
		if refUrl.Host != "waste.st" && refUrl.Host != "xn--108h.st" && refUrl.Host != "localhost:8666" {
			w.Header().Set("Location", "/r/bin")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusFound)
			return nil
		}
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Unix(int64(paste.TS), 0), bytes.NewReader(paste.Text))
	return nil
}

func serve(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data: 'self'; media-src data: 'self'; frame-src data: 'self'; frame-ancestors 'none'")

	if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
		return mutate(w, r)
	}

	w.Header().Set("Vary", "Accept, Accept-Encoding, User-Agent")

	wantsPlain := wantsPlain(r)
	if wantsPlain && len(r.URL.Path) == 1 {
		r.URL.Path = "/waste.1"
	}

	extra := ""
	lang := "auto"
	value := ""
	if len(r.URL.Path) > 1 && r.URL.Path != "/new" {
		id := r.URL.Path[1:]
		paste, err := getPaste(id)
		if err != nil {
			if err == leveldb.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				extra = "Not found or expired.\n"
				if wantsPlain {
					w.Write([]byte(extra))
					return nil
				}
			} else {
				return err
			}
		}

		if paste != nil {
			if wantsPlain {
				return serveRaw(paste, w, r)
			}

			wantSyntax := r.Header.Get("X-Syntax")
			if len(wantSyntax) > 0 {
				if len(paste.Text) < 1024*1024 {
					out, err := Pretty(string(paste.Text), wantSyntax)
					if err != nil {
						return err
					}
					w.Header().Set("Cache-Control", "no-store")
					w.Write([]byte(out))
				}
				return nil
			}

			ct := http.DetectContentType(paste.Text)

			if strings.HasPrefix(ct, "image/") {
				ct, data := embedOrURL(paste, ct)
				return tpl.Execute(w, map[string]interface{}{
					"MaxSize": *flagMaxSize,
					"Name":    paste.Name,
					"Image":   data,
					"CT":      ct,
				})
			} else if strings.HasPrefix(ct, "video/") {
				ct, data := embedOrURL(paste, ct)
				return tpl.Execute(w, map[string]interface{}{
					"MaxSize": *flagMaxSize,
					"Name":    paste.Name,
					"Video":   data,
					"CT":      ct,
				})
			} else if strings.HasPrefix(ct, "application/pdf") {
				ct, data := embedOrURL(paste, ct)
				return tpl.Execute(w, map[string]interface{}{
					"MaxSize": *flagMaxSize,
					"Name":    paste.Name,
					"IFrame":  data,
					"CT":      ct,
				})
			} else if len(paste.Text) > 0 && len(paste.Text) < 1024*1024 && !strings.HasPrefix(ct, "application/") {
				out, err := Pretty(string(paste.Text), paste.Syntax)
				if err != nil {
					return err
				}
				return tpl.Execute(w, map[string]interface{}{
					"Syntax":     template.HTML(out),
					"Name":       paste.Name,
					"MaxSize":    *flagMaxSize,
					"SyntaxList": syntaxList,
					"Language":   paste.Syntax,
				})
			} else {
				lang = "plain"
				value = string(paste.Text)
			}
		}
	}

	// Cache front page well
	w.Header().Set("Cache-Control", "s-maxage=1800, max-age=3600, stale-if-error=14400")
	return tpl.Execute(w, map[string]interface{}{
		"MaxSize":    *flagMaxSize,
		"Extra":      extra,
		"SyntaxList": syntaxList,
		"Language":   lang,
		"Value":      value,
	})
}

func mutate(w http.ResponseWriter, r *http.Request) error {
	uuid := strings.ToLower(r.Header.Get("X-UUID"))
	if uuid == "" {
		if _, pass, ok := r.BasicAuth(); ok {
			uuid = strings.ToLower(pass)
		}
	}

	cl, err := strconv.Atoi(r.Header.Get("Content-Length"))
	if err == nil && cl > *flagMaxSize {
		// Do this before reading the body, as that way PUT requests can be denied early.
		http.Error(w, "Content too big", http.StatusRequestEntityTooLarge)
		return nil
	}

	name := ""
	if r.Method == "PUT" {
		name = r.URL.Path[1:]
	} else {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
		if err == nil {
			name = params["filename"]
		}
	}
	if len(name) > 256 {
		name = name[:256]
	}

	id := r.URL.Path[1:]
	if id != "" {
		if paste, err := getPaste(id); err != nil {
			if err == leveldb.ErrNotFound {
				if r.Method != "PUT" {
					http.Error(w, "Not found", http.StatusNotFound)
					return nil
				}
				// Make a new ID for PUT requests.
				id = ""
			} else {
				return err
			}
		} else {
			// Paste exists, can requester update it?
			if paste.UUID == "" || paste.UUID != uuid {
				http.Error(w, "Invalid UUID", http.StatusForbidden)
				return nil
			}

			// Just an update?
			paste.Name = name
			if cl == 0 && len(name) > 0 {
				return paste.Save(db)
			}

			if r.Method == "DELETE" {
				w.Write([]byte("Deleted"))
				return db.Delete([]byte(id), nil)
			}
		}
	} else if r.Method == "DELETE" {
		http.Error(w, "Can't delete root.", http.StatusLocked)
		return nil
	}

	if len(id) == 0 {
		found := true
		idSize := *flagIDSize
		i := 0
		for found {
			idRaw := make([]byte, idSize)
			_, err := rand.Read(idRaw)
			if err != nil {
				return err
			}
			id = Base32Crockford.EncodeToString(idRaw)
			_, err = db.Get([]byte(id), nil)
			if err != nil {
				if err == leveldb.ErrNotFound {
					found = false
				} else {
					return err
				}
			}
			i++
			// A simple heuristic to extend IDs when needed, deliberately leaving some gaps.
			if i%32 == 0 {
				idSize++
			}
		}
	}

	limit := io.LimitReader(r.Body, int64(*flagMaxSize))
	defer r.Body.Close()
	body, err := io.ReadAll(limit)
	if err != nil {
		return err
	}
	if len(body) == *flagMaxSize {
		buf := make([]byte, 1)
		n, _ := r.Body.Read(buf)
		if n == 1 {
			// More data than the LimitReader read => over the limit.
			http.Error(w, "Content too big", http.StatusRequestEntityTooLarge)
			return nil
		}
	}

	ct := http.DetectContentType(body)
	switch ct {
	case "application/zip", "application/x-rar-compressed", "application/x-gzip":
		http.Error(w, "Content not allowed", http.StatusUnsupportedMediaType)
		return nil
	default:
		if len(body) > 32 && (string(body[:4]) == "\x7FELF" ||
			string(body[:4]) == "MZ\x90\x00" || string(body[:4]) == "MZ\x00\x00" ||
			string(body[:4]) == "\xfe\xed\xfa\xce" || string(body[:4]) == "\xfe\xed\xfa\xcf") {
			http.Error(w, "Content not allowed", http.StatusUnsupportedMediaType)
			return nil
		}
	}

	wantSyntax := r.Header.Get("X-Syntax")
	syntax := wantSyntax
	if strings.ToLower(syntax) == "auto" || syntax == "" {
		if newSyntax, ok := analyseMagic(string(body)); ok {
			syntax = newSyntax
		}
	}

	paste := &Paste{
		id:     id,
		UUID:   uuid,
		Text:   body,
		Name:   name,
		Syntax: syntax,
		TS:     int(time.Now().Unix()),
	}
	if err := paste.Save(db); err != nil {
		return err
	}

	uri := paste.URI(r)
	w.Header().Set("Content-Location", uri.Path)

	if wantSyntax != "" {
		out, err := Pretty(string(paste.Text), syntax)
		if err != nil {
			return err
		}
		w.Write([]byte(out))
	} else {
		w.Write([]byte(fmt.Sprintf("%s://%s%s\n", uri.Scheme, uri.Host, uri.Path)))
	}

	return nil
}

func getPaste(id string) (*Paste, error) {
	value, err := db.Get([]byte(id), nil)
	if err != nil {
		return nil, err
	}
	var paste Paste
	if err := json.NewDecoder(bytes.NewReader(value)).Decode(&paste); err != nil {
		return nil, err
	}
	paste.id = id
	return &paste, nil
}

func (p *Paste) Save(db *leveldb.DB) error {
	var js bytes.Buffer
	if err := json.NewEncoder(&js).Encode(p); err != nil {
		return err
	}

	log.Print(p.id)
	return db.Put([]byte(p.id), js.Bytes(), nil)
}

func (p *Paste) URI(r *http.Request) *url.URL {
	uri := &url.URL{
		Scheme: "https",
		Host:   r.Host,
		Path:   "/" + p.id,
	}
	if strings.Contains(uri.Host, ":") {
		// Development running on a port, no https
		uri.Scheme = "http"
	}
	if strings.Contains(uri.Host, "xn--") && len(*flagWasteHost) > 0 {
		uri.Host = *flagWasteHost
	}
	return uri
}

func errorWrap(f func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		err := f(w, r)
		if err != nil {
			log.Print(err)
			http.Error(w, "Error", http.StatusInternalServerError)
		}
	}
}

func wantsPlain(req *http.Request) bool {
	accepts := strings.Split(req.Header.Get("Accept"), ",")
ACCEPT:
	for _, accept := range accepts {
		switch i := strings.Split(strings.TrimSpace(accept), ";"); i[0] {
		case "text/html":
			return false
		case "text/plain":
			return true
		case "*/*":
			break ACCEPT
		}
	}
	ua := req.Header.Get("User-Agent")
	if strings.Contains(ua, "curl/") || strings.Contains(ua, "Wget/") {
		return true
	}
	if !strings.Contains(ua, "/") && len(accepts) == 1 && accepts[0] == "" {
		// No Accept header and User-Agent doesn't look like a real browser.
		return true
	}
	return false
}

func embedOrURL(paste *Paste, ct string) (string, string) {
	if len(paste.Text) > 256*1024 {
		return "", "/r/" + paste.id
	} else {
		return ct, base64.StdEncoding.EncodeToString(paste.Text)
	}
}
