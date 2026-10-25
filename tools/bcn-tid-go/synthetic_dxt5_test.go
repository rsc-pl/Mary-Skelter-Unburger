package main

import (
 "encoding/binary"
 "testing"
 "github.com/woozymasta/bcn"
 "maryskelter-unburger/internal/mskit"
)

func tinyDXT5(t *testing.T)([]byte,[]byte){
 t.Helper()
 pixels:=make([]byte,8*8*4)
 for i:=0;i<64;i++{pixels[i*4],pixels[i*4+1],pixels[i*4+2],pixels[i*4+3]=60,120,180,255}
 blocks,e:=bcn.EncodeBC3WithOptions(pixels,8,8,&bcn.EncodeOptions{QualityLevel:6,Workers:1})
 if e!=nil{t.Fatal(e)}
 head:=make([]byte,128);copy(head[:4],[]byte{'T','I','D',0x91})
 binary.BigEndian.PutUint32(head[0x44:],8);binary.BigEndian.PutUint32(head[0x48:],8)
 copy(head[0x64:],"5TXD")
 tid:=append(head,blocks...)
 src,meta,e:=mskit.DecodeTIDDXT5RGBA(tid)
 if e!=nil || meta.Format!="DXT5"{t.Fatalf("DXT5 meta %+v err %v",meta,e)}
 return tid,src
}
