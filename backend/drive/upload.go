// Upload for drive
//
// Docs
// Resumable upload: https://developers.google.com/drive/web/manage-uploads#resumable
// Best practices: https://developers.google.com/drive/web/manage-uploads#best-practices
// Files insert: https://developers.google.com/drive/v2/reference/files/insert
// Files update: https://developers.google.com/drive/v2/reference/files/update
//
// This contains code adapted from google.golang.org/api (C) the GO AUTHORS

package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	//"github.com/glycerine/rbuf"
	"github.com/smallnest/ringbuffer"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

const (
	// statusResumeIncomplete is the code returned by the Google uploader when the transfer is not yet complete.
	statusResumeIncomplete = 308
)

// resumableUpload is used by the generated APIs to provide resumable uploads.
// It is not used by developers directly.
type resumableUpload struct {
	f      *Fs
	remote string
	// URI is the resumable resource destination provided by the server after specifying "&uploadType=resumable".
	URI string
	// Media is the object being uploaded.
	Media io.Reader
	// MediaType defines the media type, e.g. "image/jpeg".
	MediaType string
	// ContentLength is the full size of the object being uploaded.
	ContentLength int64
	// Return value
	ret *drive.File
}

// Upload the io.Reader in of size bytes with contentType and info
func (f *Fs) Upload(ctx context.Context, in io.Reader, size int64, contentType, fileID, remote string, info *drive.File) (*drive.File, error) {
	params := url.Values{
		"alt":        {"json"},
		"uploadType": {"resumable"},
		"fields":     {partialFields},
	}
	params.Set("supportsAllDrives", "true")
	if f.opt.KeepRevisionForever {
		params.Set("keepRevisionForever", "true")
	}
	urls := "https://www.googleapis.com/upload/drive/v3/files"
	method := "POST"
	if fileID != "" {
		params.Set("setModifiedDate", "true")
		urls += "/{fileId}"
		method = "PATCH"
	}
	urls += "?" + params.Encode()
	var res *http.Response
	var err error
	err = f.pacer.Call(func() (bool, error) {
		var body io.Reader
		body, err = googleapi.WithoutDataWrapper.JSONReader(info)
		if err != nil {
			return false, err
		}
		var req *http.Request
		req, err = http.NewRequest(method, urls, body)
		if err != nil {
			return false, err
		}
		req = req.WithContext(ctx) // go1.13 can use NewRequestWithContext
		googleapi.Expand(req.URL, map[string]string{
			"fileId": fileID,
		})
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		req.Header.Set("X-Upload-Content-Type", contentType)
		if size >= 0 {
			req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%v", size))
		}
		res, err = f.client.Do(req)
		if err == nil {
			defer googleapi.CloseBody(res)
			err = googleapi.CheckResponse(res)
		}
		return f.shouldRetry(err)
	})
	if err != nil {
		return nil, err
	}
	loc := res.Header.Get("Location")
	rx := &resumableUpload{
		f:             f,
		remote:        remote,
		URI:           loc,
		Media:         in,
		MediaType:     contentType,
		ContentLength: size,
	}
	return rx.Upload(ctx)
}

// Make an http.Request for the range passed in
func (rx *resumableUpload) makeRequest(ctx context.Context, start int64, body io.ReadSeeker, reqSize int64) *http.Request {
	req, _ := http.NewRequest("POST", rx.URI, body)
	req = req.WithContext(ctx) // go1.13 can use NewRequestWithContext
	req.ContentLength = reqSize
	totalSize := "*"
	if rx.ContentLength >= 0 {
		totalSize = strconv.FormatInt(rx.ContentLength, 10)
	}
	if reqSize != 0 {
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %v-%v/%v", start, start+reqSize-1, totalSize))
	} else {
		req.Header.Set("Content-Range", fmt.Sprintf("bytes */%v", totalSize))
	}
	//fs.Debugf(rx.remote, "reqSize: %s", req.Header["Content-Range"])
	req.Header.Set("Content-Type", rx.MediaType)
	return req
}

// Transfer a chunk - caller must call googleapi.CloseBody(res) if err == nil || res != nil
func (rx *resumableUpload) transferChunk(ctx context.Context, start int64, chunk io.ReadSeeker, chunkSize int64) (int, error) {
	_, _ = chunk.Seek(0, io.SeekStart)
	req := rx.makeRequest(ctx, start, chunk, chunkSize)
	res, err := rx.f.client.Do(req)
	if err != nil {
		return 599, err
	}
	defer googleapi.CloseBody(res)
	if res.StatusCode == statusResumeIncomplete {
		return res.StatusCode, nil
	}
	err = googleapi.CheckResponse(res)
	if err != nil {
		return res.StatusCode, err
	}

	// When the entire file upload is complete, the server
	// responds with an HTTP 201 Created along with any metadata
	// associated with this resource. If this request had been
	// updating an existing entity rather than creating a new one,
	// the HTTP response code for a completed upload would have
	// been 200 OK.
	//
	// So parse the response out of the body.  We aren't expecting
	// any other 2xx codes, so we parse it unconditionally on
	// StatusCode
	if err = json.NewDecoder(res.Body).Decode(&rx.ret); err != nil {
		return 598, err
	}

	return res.StatusCode, nil
}

// min returns the smallest of x, y
func min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

/*
type doubleReadSeeker struct {
	rs1, rs2       io.ReadSeeker
	rs1len, rs2len int64
	second         bool
	pos            int64
}

func (r *doubleReadSeeker) Seek(offset int64, whence int) (int64, error) {
	//log.Printf("Seek %d %d", offset, whence)
	var err error
	switch whence {
	case os.SEEK_SET:
		if offset < r.rs1len {
			r.second = false
			r.pos, err = r.rs1.Seek(offset, os.SEEK_SET)
			r.pos, err = r.rs2.Seek(0, os.SEEK_SET)
			return r.pos, err
		} else {
			r.second = true
			r.pos, err = r.rs2.Seek(offset-r.rs1len, os.SEEK_SET)
			r.pos += r.rs1len
			return r.pos, err
		}
	case os.SEEK_END: // negative offset
		return r.Seek(r.rs1len+r.rs2len+offset-1, os.SEEK_SET)
	default: // os.SEEK_CUR
		return r.Seek(r.pos+offset, os.SEEK_SET)
	}
}

func (r *doubleReadSeeker) Read(p []byte) (n int, err error) {
	//log.Printf("Read %d %d", len(p), r.pos)
	switch {
	case r.pos >= r.rs1len: // read only from the second reader
		n, err := r.rs2.Read(p)
		r.pos += int64(n)
		//log.Printf("<Read1 %d %d %s", n, r.pos, err)
		return n, err
	case r.pos+int64(len(p)) <= r.rs1len: // read only from the first reader
		n, err := r.rs1.Read(p)
		r.pos += int64(n)
		//log.Printf("<Read2 %d %d %s", n, r.pos, err)
		return n, err
	default: // read on the border - end of first reader and start of second reader
		n1, err := r.rs1.Read(p)
		r.pos += int64(n1)
		if r.pos != r.rs1len || (err != nil && err != io.EOF) {
			// Read() might not read all, return
			// If error (but not EOF), return
			return n1, err
		}
		_, err = r.rs2.Seek(0, os.SEEK_SET)
		if err != nil {
			return n1, err
		}
		r.second = true
		n2, err := r.rs2.Read(p[n1:])
		r.pos += int64(n2)
		//log.Printf("<Read3 %d %d %s", n, r.pos, err)
		return n1 + n2, err
	}
}

func multiReadSeeker(rs []io.ReadSeeker, leftmost bool) (io.ReadSeeker, int64, error) {
	if len(rs) == 1 {
		r := rs[0]
		l, err := r.Seek(0, os.SEEK_END)
		if err != nil {
			return nil, 0, err
		}
		if leftmost {
			_, err = r.Seek(0, os.SEEK_SET)
		}
		return r, l, err
	} else {
		rs1, l1, err := multiReadSeeker(rs[:len(rs)/2], leftmost)
		if err != nil {
			return nil, 0, err
		}
		rs2, l2, err := multiReadSeeker(rs[len(rs)/2:], true)
		if err != nil {
			return nil, 0, err
		}
		return &doubleReadSeeker{rs1, rs2, l1, l2, false, 0}, l1 + l2, nil
	}
}

type emptyReadSeeker struct{}

func (r *emptyReadSeeker) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (r *emptyReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, io.EOF
}

// MultiReadSeeker returns a ReadSeeker that's the logical concatenation of the provided
// input readseekers. After calling this method the initial position is set to the
// beginning of the first ReadSeeker. At the end of a ReadSeeker, Read always advances
// to the beginning of the next ReadSeeker and returns EOF at the end of the last ReadSeeker.
// Seek can be used over the sum of lengths of all readseekers.
//
// When a MultiReadSeeker is used, no Read and Seek operations should be made on
// its ReadSeeker components and the length of the readseekers should not change.
// Also, users should make no assumption on the state of individual readseekers
// while the MultiReadSeeker is used.
func MultiReadSeeker(start int64, rs ...io.ReadSeeker) io.ReadSeeker {
	if len(rs) == 0 {
		return &emptyReadSeeker{}
	}
	r, len, err := multiReadSeeker(rs, true)
	if err != nil {
		return &emptyReadSeeker{}
	}
	if start != 0 {
		return &doubleReadSeeker{bytes.NewReader(make([]byte, 0)), r, start, len, false, 0}
	}
	return r
}
*/

type StubReadSeeker struct {
	//f *ringbuffer.RingBuffer
	buf1 []byte
	buf2 []byte
	size int64
	buf  io.Reader
}

func (r *StubReadSeeker) Read(p []byte) (n int, err error) {
	return r.buf.Read(p)
}

func (r *StubReadSeeker) Seek(offset int64, whence int) (int64, error) {
	//b := r.f.BytesTwo()
	//r.buf = bytes.NewBuffer(b[0:min(int64(len(b)), r.size)])
	r.buf = io.LimitReader(io.MultiReader(bytes.NewBuffer(r.buf1), bytes.NewBuffer(r.buf2)), r.size)
	return 0, nil
}

func NewStubReadSeeker(buf *ringbuffer.RingBuffer, size int64) *StubReadSeeker {
	two := buf.BytesTwo()
	return &StubReadSeeker{buf1: two.First, buf2: two.Second, size: size}
}

var openFileMap sync.Map

func init() {
	go func() {
		for {
			fl := map[string]int{}
			openFileMap.Range(func(k interface{}, v interface{}) bool {
				fl[k.(string)] = v.(int)
				return true
			})
			if len(fl) > 0 {
				log.Printf("Opened files: %s", fl)
			}
			time.Sleep(time.Second * 25)
		}
	}()
}

// Upload uploads the chunks from the input
// It retries each chunk using the pacer and --low-level-retries
func (rx *resumableUpload) Upload(ctx context.Context) (*drive.File, error) {
	var StatusCode int
	var err error

	fs.Infof(rx.remote, "ChunkSize: %v", int(rx.f.opt.ChunkSize))
	//buf := rbuf.NewAtomicFixedSizeRingBuf(int(rx.f.opt.ChunkSize))
	INITBUFSIZE := 6 * 1024 * 1024
	SMALLCHUNK := fs.SizeSuffix(32 * 1024)
	MAXBUFSIZE := int(rx.f.opt.ChunkSize)
	key := rx.remote + "_" + strconv.Itoa(int(time.Now().Unix()))
	buf := ringbuffer.New(INITBUFSIZE)
	openFileMap.Store(key, INITBUFSIZE)
	//buf := make([]byte, int(rx.f.opt.ChunkSize))
	var finished bool
	errReaderChan := make(chan error)
	errChan := make(chan error)
	//var readerMx sync.Mutex
	var bufchangeMx sync.Mutex
	pos := int64(0)
	fullLen := int64(0)
	go func() {
		tempbuf := make([]byte, SMALLCHUNK)
		for {
			n, err := rx.Media.Read(tempbuf)
			wbuf := tempbuf[:n]

			for {
				bufchangeMx.Lock()
				wn, err := buf.Write(wbuf)
				bufchangeMx.Unlock()
				wbuf = wbuf[wn:]
				if err == io.EOF {
					time.Sleep(500 * time.Millisecond)
				}
				if len(wbuf) == 0 {
					break
				}
			}

			pos += int64(n)
			if err != nil {
				fullLen = pos
				finished = true
				if err == io.EOF {
					// Send the last chunk with the correct ContentLength
					// otherwise Google doesn't know we've finished
					errReaderChan <- nil
					return
				} else {
					errReaderChan <- err
					return
				}
			}
		}
	}()

	go func() {
		//testF, _ := os.Create("test")
		//defer testF.Close()
		start := int64(0)
		overtime := 0
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			reqSize := int64(buf.Readable())
			if int(reqSize) > buf.Capacity()-262144 {
				overtime++
				if overtime > 2 {
					bufchangeMx.Lock()
					newsize := buf.Capacity() + 2*1024*1024
					if newsize > MAXBUFSIZE {
						newsize = MAXBUFSIZE
					}
					if newsize != buf.Capacity() && newsize <= MAXBUFSIZE {
						fs.Logf(rx.remote, "ChunkSize increase to %d", newsize)
						newbuf := ringbuffer.New(newsize)
						//newbuf := rbuf.NewAtomicFixedSizeRingBuf(buf.N + 1 * 1024 * 1024)
						ret := func() error {
							two := buf.BytesTwo()
							_, err := newbuf.Write(two.First)
							if err != nil {
								return err
							}
							_, err = newbuf.Write(two.Second)
							if err != nil {
								return err
							}
							return nil
						}()
						if ret == nil {
							buf = newbuf
							openFileMap.Store(key, newsize)
							if buf.Readable() != newbuf.Readable() {
								panic("Unexpected transfer fail ...")
							}
						}
					}
					overtime = 0
					bufchangeMx.Unlock()
				}
			} else {
				if overtime >= -5 {
					overtime--
				}
			}
			if !finished {
				if reqSize < 262144 { // fuck Google
					time.Sleep(500 * time.Millisecond)
					continue
				} else if reqSize < 1024*1024 /*int64(buf.Capacity()) / 3*/ {
					//fs.Debugf(rx.remote, "ReqSize too small: %v", reqSize)
					time.Sleep(500 * time.Millisecond)
					continue
				}
				reqSize -= reqSize % 262144
			} else {
				if start+reqSize != fullLen {
					fs.Errorf(rx.remote, "Error size not match: start + reqSize = %v, fullLen = %v", start+reqSize, fullLen)
				}
				rx.ContentLength = fullLen
			}
			var chunk io.ReadSeeker
			//chunk = &StubReadSeeker{f:buf, size: reqSize}
			chunk = NewStubReadSeeker(buf, reqSize)
			//chunk.Seek(0, io.SeekStart)

			//n, err := io.Copy(testF, chunk)
			//fs.Infof(rx.remote, "Test Read: %d %s", n, err)

			//if finished && start + reqSize == fullLen {
			//	rx.ContentLength = fullLen
			//}
			// Transfer the chunk
			err = rx.f.pacer.Call(func() (bool, error) {
				s := time.Now()
				StatusCode, err = rx.transferChunk(ctx, start, chunk, reqSize)
				diffT := time.Now().Sub(s)
				if diffT > 1000*time.Millisecond {
					fs.Infof(rx.remote, "Sent chunk %d length %d, Code: %d, Err: %s, Time %s", start, reqSize, StatusCode, err, diffT)
				}
				again, err := rx.f.shouldRetry(err)
				if StatusCode == statusResumeIncomplete || StatusCode == http.StatusCreated || StatusCode == http.StatusOK {
					again = false
					err = nil
				}
				return again, err
			})
			if err != nil {
				errChan <- err
				return
			}
			start += reqSize
			buf.Advance(int(reqSize))
			//fs.Infof(rx.remote, "%v  %v", start, fullLen)
			if finished && start >= fullLen {
				errChan <- nil
				return
			}
			//time.Sleep(time.Second * 3)
			<-ticker.C
		}
	}()
	/*go func() {
		time.Sleep(100 * time.Millisecond)
		for {
			var n int64
			var newpos int64
			readSize := int64(0)
			log.Printf("buf start: %d end %d", bufstartpos, bufendpos)
			readerMx.Lock()
			newpos = min(int64(bufstartpos) + int64(len(buf)) - 1, int64(bufendpos + SMALLCHUNK))
			if newpos == int64(bufendpos) {
				time.Sleep(10 * time.Millisecond)
				readerMx.Unlock()
				continue
			}
			readSize = newpos - int64(bufendpos)
			s := bufendpos % fs.SizeSuffix(len(buf))
			e := newpos % int64(len(buf))
			var bufSlice io.Writer
			if int64(s) > e {
				bufSlice = io.MultiWriter(bytes.NewBuffer(buf[s:]), bytes.NewBuffer(buf[0:e]))
			} else {
				bufSlice = bytes.NewBuffer(buf[s:e])
			}
			readerMx.Unlock()

			n, err = io.CopyN(bufSlice, rx.Media, readSize)
			//n, err = readers.ReadFill(rx.Media, bufSlice)
			readerMx.Lock()
			bufendpos += fs.SizeSuffix(n)
			readerMx.Unlock()
			if err == io.EOF {
				// Send the last chunk with the correct ContentLength
				// otherwise Google doesn't know we've finished
				rx.ContentLength = int64(bufendpos)
				finished = true
				return
			} else if err != nil {
				errChan <- err
				return
			}
		}
	}()

	go func() {
		testF, _ := os.Create("test")
		defer testF.Close()
		start := int64(0)
		for {
			var reqSize int64
			var chunk io.ReadSeeker
			readerMx.Lock()
			_s := bufstartpos
			_e := bufendpos
			_e -= (_e - _s) % 262144 // fuck google...
			readerMx.Unlock()
			if _e == _s {
				chunk = nil
				reqSize = 0
				time.Sleep(100 * time.Millisecond)
				continue
			}
			s := _s % fs.SizeSuffix(len(buf))
			e := _e % fs.SizeSuffix(len(buf))
			fs.Infof(rx.remote, "Sending buf %d to %d", s, e)
			if e < s {
				chunk = MultiReadSeeker(0, bytes.NewReader(buf[s:]), bytes.NewReader(buf[0:e]))
				reqSize = int64(fs.SizeSuffix(len(buf)) - s + e)
			} else {
				chunk = bytes.NewReader(buf[s:e])
				reqSize = int64(e-s)
			}
			//chunk.Seek(0, io.SeekStart)
			//chunk.Seek(int64(_s), io.SeekStart)
			//n, err := chunk.Read(make([]byte, 0x1000000))
			//fs.Infof(rx.remote, "Test Read: %d %s", n, err)
			if reqSize < 262144 { // fuck Google
				continue
			}
			n, err := io.Copy(testF, chunk)
			fs.Infof(rx.remote, "Test Read: %d %s", n, err)

			// Transfer the chunk
			err = rx.f.pacer.Call(func() (bool, error) {
				fs.Infof(rx.remote, "Sending chunk %d length %d", start, reqSize)
				StatusCode, err = rx.transferChunk(ctx, start, chunk, reqSize)
				fs.Infof(rx.remote, "Err: %s", err)
				again, err := rx.f.shouldRetry(err)
				if StatusCode == statusResumeIncomplete || StatusCode == http.StatusCreated || StatusCode == http.StatusOK {
					again = false
					err = nil
				}
				return again, err
			})
			if err != nil {
				errChan <- err
				return
			}
			readerMx.Lock()
			bufstartpos += fs.SizeSuffix(reqSize)
			readerMx.Unlock()
			start += reqSize
			if finished && start == rx.ContentLength {
				errChan <- nil
			}
		}
	}()*/

goodexit:
	for {
		var errReader error
		select {
		case errReader = <-errReaderChan:
			fs.Infof(rx.remote, "Reader finished with error: %s", errReader)
		case err := <-errChan:
			fs.Infof(rx.remote, "Writer finished with error: %s", err)
			if errReader != nil {
				return nil, errReader
			}
			if err != nil {
				return nil, err
			}
			break goodexit
		}
	}
	openFileMap.Delete(key)
	// Resume or retry uploads that fail due to connection interruptions or
	// any 5xx errors, including:
	//
	// 500 Internal Server Error
	// 502 Bad Gateway
	// 503 Service Unavailable
	// 504 Gateway Timeout
	//
	// Use an exponential backoff strategy if any 5xx server error is
	// returned when resuming or retrying upload requests. These errors can
	// occur if a server is getting overloaded. Exponential backoff can help
	// alleviate these kinds of problems during periods of high volume of
	// requests or heavy network traffic.  Other kinds of requests should not
	// be handled by exponential backoff but you can still retry a number of
	// them. When retrying these requests, limit the number of times you
	// retry them. For example your code could limit to ten retries or less
	// before reporting an error.
	//
	// Handle 404 Not Found errors when doing resumable uploads by starting
	// the entire upload over from the beginning.
	if rx.ret == nil {
		return nil, fserrors.RetryErrorf("Incomplete upload - retry, last error %d", StatusCode)
	}
	return rx.ret, nil
}
