package main

// Native offline PNG -> changed-block BC7/DXT5 TID -> CL3/GDAT batch.
// All assets are source-hash-bound and every output is staged before publication.
// No Python/Pillow, cgo, game install or production archive modification.
import (
    "bytes"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "image"
    "image/color"
    _ "image/png"
    "io"
    "os"
    "path/filepath"
    "strings"

    "maryskelter-unburger/internal/mskit"
)

type GDATPNGEdit struct {
    GDATIndex int `json:"gdat_index"`
    Inner string `json:"inner"`
    SourceTIDSHA256 string `json:"source_tid_sha256"`
    TargetPNG string `json:"target_png"`
    TargetPNGSHA256 string `json:"target_png_sha256"`
    Quality int `json:"quality,omitempty"`
    MaxAlpha *int `json:"max_alpha,omitempty"`
    MaxVisible *int `json:"max_visible,omitempty"`
}
type GDATPNGJob struct {
    Source string `json:"source"`
    SourceSHA256 string `json:"source_sha256"`
    Output string `json:"output"`
    Edits []GDATPNGEdit `json:"edits"`
}
type GDATPNGManifest struct {
    Version int `json:"version"`
    Jobs []GDATPNGJob `json:"jobs"`
}
type GDATPNGResult struct {
    Source string `json:"source"`
    Output string `json:"output"`
    SourceSHA256 string `json:"source_sha256"`
    OutputSHA256 string `json:"output_sha256"`
    TIDs []BC7Report `json:"tids"`
}
type GDATPNGResponse struct {
    Status string `json:"status"`
    Jobs []GDATPNGResult `json:"jobs"`
}
type stagedGDAT struct { tmp, dest string; result GDATPNGResult }

func checkedPath(base, value string) (string, error) {
    if value == "" || strings.TrimSpace(value) != value { return "", errors.New("empty/invalid path") }
    if !filepath.IsAbs(value) { value = filepath.Join(base, value) }
    return filepath.Abs(filepath.Clean(value))
}
func checkedHash(b []byte, expected string) bool {
    if len(expected) != 64 { return false }
    h, err := hex.DecodeString(expected)
    if err != nil || len(h) != sha256.Size { return false }
    digest := sha256.Sum256(b)
    return bytes.Equal(digest[:], h)
}
func sourceRGBAfromPNG(data []byte, w, h int) ([]byte, error) {
    img, kind, err := image.Decode(bytes.NewReader(data))
    if err != nil { return nil, err }
    if kind != "png" || img.Bounds().Dx()!=w || img.Bounds().Dy()!=h { return nil, errors.New("PNG format/dimensions mismatch") }
    out := make([]byte, w*h*4)
    b := img.Bounds()
    for y:=0; y<h; y++ { for x:=0; x<w; x++ {
        c:=color.NRGBAModel.Convert(img.At(b.Min.X+x,b.Min.Y+y)).(color.NRGBA)
        i:=(y*w+x)*4; out[i],out[i+1],out[i+2],out[i+3]=c.R,c.G,c.B,c.A
    } }
    return out,nil
}
func parsePNGManifest(blob []byte) (GDATPNGManifest,error) {
    var m GDATPNGManifest
    d:=json.NewDecoder(bytes.NewReader(blob));d.DisallowUnknownFields()
    if err:=d.Decode(&m);err!=nil{return m,err}
    var tail any
    if err:=d.Decode(&tail);err!=io.EOF{return m,errors.New("trailing manifest data")}
    if m.Version!=1 || len(m.Jobs)<1 || len(m.Jobs)>32{return m,errors.New("manifest version=1, jobs=1..32 required")}
    for i,j:=range m.Jobs{
        if len(j.Edits)<1 || len(j.Edits)>128 {return m,fmt.Errorf("job %d edits out of bounds",i)}
        seen:=map[string]bool{}
        for k,e:=range j.Edits{
            if e.GDATIndex<0 || e.Inner=="" || strings.TrimSpace(e.Inner)!=e.Inner || !strings.HasSuffix(strings.ToLower(e.Inner),".tid") {return m,fmt.Errorf("invalid TID target %d/%d",i,k)}
            id:=fmt.Sprintf("%d/%s",e.GDATIndex,strings.ToLower(e.Inner))
            if seen[id] {return m,fmt.Errorf("duplicate TID target %s",id)}
            seen[id]=true
            if e.Quality<0 || e.Quality>10 {return m,errors.New("quality must be 0 (default) or 1..10")}
            for _,v:=range []*int{e.MaxAlpha,e.MaxVisible} {if v!=nil && (*v<0 || *v>255){return m,errors.New("quality guard out of bounds")}}
        }
    }
    return m,nil
}
// RunGDATPNGBatchWithHook supports deterministic fault injection to prove
// published-output rollback. No output ever overwrites a preexisting path.
func RunGDATPNGBatchWithHook(manifestPath string, beforePublish func(int)error) (_ GDATPNGResponse, err error) {
    var response GDATPNGResponse
    path,err:=filepath.Abs(manifestPath);if err!=nil{return response,err}
    raw,err:=os.ReadFile(path);if err!=nil{return response,err}
    manifest,err:=parsePNGManifest(raw);if err!=nil{return response,err}
    base:=filepath.Dir(path)
    staged:=[]stagedGDAT{}
    published:=[]string{}
    defer func(){
        for _,v:=range staged{ if v.tmp!="" {_=os.Remove(v.tmp)} }
        if err!=nil {for i:=len(published)-1;i>=0;i-- {_=os.Remove(published[i])} }
    }()
    usedSrc:=map[string]bool{};usedDst:=map[string]bool{}
    for n,job:=range manifest.Jobs{
        src,e:=checkedPath(base,job.Source);if e!=nil{return response,e}
        dst,e:=checkedPath(base,job.Output);if e!=nil{return response,e}
        if src==dst || usedSrc[src] || usedDst[dst] {return response,fmt.Errorf("job %d path alias/duplicate",n)}
        usedSrc[src]=true;usedDst[dst]=true
        if _,e:=os.Lstat(dst); e==nil{return response,fmt.Errorf("output already exists: %s",dst)}else if !os.IsNotExist(e){return response,e}
        original,e:=os.ReadFile(src);if e!=nil{return response,e}
        if !checkedHash(original,job.SourceSHA256){return response,fmt.Errorf("job %d source archive SHA mismatch",n)}
        originalGDAT,e:=mskit.ParseGDAT(original);if e!=nil{return response,e}
        result:=GDATPNGResult{Source:src,Output:dst,SourceSHA256:digest(original)}
        updated:=original
        touched:=map[int]bool{}
        for k,edit:=range job.Edits{
            pristine,_,e:=mskit.ExtractGraphicsTID(original,edit.GDATIndex,edit.Inner)
            if e!=nil{return response,fmt.Errorf("job %d edit %d extract: %w",n,k,e)}
            if !checkedHash(pristine,edit.SourceTIDSHA256){return response,fmt.Errorf("job %d edit %d TID SHA mismatch",n,k)}
            meta,e:=mskit.ParseTIDMeta(pristine);if e!=nil{return response,e}
            p,e:=checkedPath(base,edit.TargetPNG);if e!=nil{return response,e}
            data,e:=os.ReadFile(p);if e!=nil{return response,e}
            if !checkedHash(data,edit.TargetPNGSHA256){return response,fmt.Errorf("job %d edit %d PNG SHA mismatch",n,k)}
            rgba,e:=sourceRGBAfromPNG(data,meta.Width,meta.Height);if e!=nil{return response,e}
            q:=edit.Quality;if q==0{q=6}
            a,v:=64,64;if edit.MaxAlpha!=nil{a=*edit.MaxAlpha};if edit.MaxVisible!=nil{v=*edit.MaxVisible}
            tid,rep,e:=EncodeChangedTID(pristine,rgba,q,a,v);if e!=nil{return response,fmt.Errorf("job %d edit %d encode: %w",n,k,e)}
            updated,_,e=mskit.PatchGraphicsTIDGoSafe(updated,edit.GDATIndex,edit.Inner,tid)
            if e!=nil{return response,fmt.Errorf("job %d edit %d patch: %w",n,k,e)}
            extracted,_,e:=mskit.ExtractGraphicsTID(updated,edit.GDATIndex,edit.Inner)
            if e!=nil || !bytes.Equal(extracted,tid){return response,fmt.Errorf("job %d edit %d roundtrip failure: %v",n,k,e)}
            touched[edit.GDATIndex]=true;result.TIDs=append(result.TIDs,rep)
        }
        recheck,e:=mskit.ParseGDAT(updated);if e!=nil{return response,e}
        if len(recheck.Entries)!=len(originalGDAT.Entries){return response,errors.New("GDAT record count drift")}
        for i:=range originalGDAT.Entries{
            if !touched[i] && !bytes.Equal(originalGDAT.Entries[i],recheck.Entries[i]){return response,fmt.Errorf("untouched GDAT entry %d drift",i)}
        }
        tmp,e:=os.CreateTemp(filepath.Dir(dst),".bcn-gdat-*.tmp");if e!=nil{return response,e}
        tmpName:=tmp.Name();staged=append(staged,stagedGDAT{tmp:tmpName,dest:dst})
        if _,e=tmp.Write(updated);e!=nil{tmp.Close();return response,e}
        if e=tmp.Sync();e!=nil{tmp.Close();return response,e}
        if e=tmp.Close();e!=nil{return response,e}
        result.OutputSHA256=digest(updated)
        staged[len(staged)-1].result=result
    }
    for _,v:=range staged{if usedSrc[v.dest]{return response,errors.New("output aliases another batch source")}}
    for i,v:=range staged{
        if beforePublish!=nil{if e:=beforePublish(i);e!=nil{return response,e}}
        if e:=os.Link(v.tmp,v.dest);e!=nil{return response,e}
        published=append(published,v.dest)
        response.Jobs=append(response.Jobs,v.result)
    }
    response.Status="PASS_NATIVE_GO_BC7_DXT5_PNG_TO_GDAT_BATCH"
    return response,nil
}
func RunGDATPNGBatch(path string)(GDATPNGResponse,error){return RunGDATPNGBatchWithHook(path,nil)}
