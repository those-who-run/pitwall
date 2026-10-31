package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"
)

// noCache is the data answers' Cache-Control: the browser keeps the body and asks
// again every time, and a 304 is cheap (v1's NO_CACHE).
const noCache = "no-cache"

// viewKeyLen is the length of the view part of an ETag, in hex digits.
const viewKeyLen = 16

// view is one GET of a data method, with what makes its answer a 304. Its ETag is
// weak (the gzipped and plain bodies are the same resource, as in v1) and
// carries two parts: a key of the pit wall's version, the procedure and the
// request, so a new pit wall or another filter never matches an old body; and
// logbook's data_version for the answer. A browser sending the ETag back hands
// the pit wall the data_version it has, which goes to logbook as
// known_data_version, so a 5 s poll costs logbook next to nothing when nothing
// changed (contracts#41).
type view struct {
	info  *connect.CallInfo
	get   bool
	key   string
	known *string
}

// newView reads the request's If-None-Match for this procedure and request. A
// POST, or a call not over HTTP, is never answered 304 and sends no
// known_data_version.
func newView(ctx context.Context, version, procedure string, req proto.Message) *view {
	v := &view{}
	v.info, _ = connect.CallInfoForServerContext(ctx)
	if server, ok := connecthttp.ServerInfoForContext(ctx); ok {
		v.get = server.HTTPMethod() == http.MethodGet
	}
	// Deterministic, so the same request gives the same key; an error leaves the
	// key of the procedure alone, which only costs a 304.
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	sum := sha256.Sum256([]byte(version + "\x00" + procedure + "\x00" + string(body)))
	v.key = hex.EncodeToString(sum[:])[:viewKeyLen]
	if v.get && v.info != nil {
		v.known = knownVersion(strings.Join(v.info.RequestHeader().Values("If-None-Match"), ","), v.key)
	}
	return v
}

// knownVersion is the data_version in the first of If-None-Match's tags that is
// this view's, or nil.
func knownVersion(ifNoneMatch, key string) *string {
	for tag := range strings.SplitSeq(ifNoneMatch, ",") {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
		tag = strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`)
		if dv, ok := strings.CutPrefix(tag, key+":"); ok && dv != "" {
			return &dv
		}
	}
	return nil
}

// etag is the ETag for an answer with logbook's dataVersion, or "" when the
// version can't sit in an ETag (an empty one, or one with a quote or a byte
// outside visible ASCII), so the answer is sent with none.
func (v *view) etag(dataVersion string) string {
	if dataVersion == "" {
		return ""
	}
	for i := range len(dataVersion) {
		if c := dataVersion[i]; c <= ' ' || c == '"' || c > '~' {
			return ""
		}
	}
	return `W/"` + v.key + ":" + dataVersion + `"`
}

// answer sets the answer's ETag and Cache-Control from logbook's dataVersion, and
// returns the 304 to send instead of a body when logbook said unchanged or the
// browser already has this ETag. nil means send the body.
func (v *view) answer(dataVersion string, unchanged bool) error {
	if v.info == nil {
		return nil
	}
	tag := v.etag(dataVersion)
	if tag == "" {
		return nil
	}
	v.info.ResponseHeader().Set("ETag", tag)
	v.info.ResponseHeader().Set("Cache-Control", noCache)
	if v.get && (unchanged || (v.known != nil && *v.known == dataVersion)) {
		return connecthttp.NewNotModifiedError()
	}
	return nil
}
