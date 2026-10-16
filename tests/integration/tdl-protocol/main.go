// This isolated test executable speaks the pinned tdl CLI/file protocol.
// Production configuration always uses the official tdl executable.
package main

import (
 "encoding/json"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/url"
 "os"
 "path/filepath"
 "strings"
 "time"
)

func main(){if err:=run();err!=nil{fmt.Fprintln(os.Stderr,"isolated tdl protocol failed");os.Exit(1)}}
func run()error{
 args:=os.Args[1:]
 contains:=func(word string)bool{for _,arg:=range args{if arg==word{return true}};return false}
 if contains("version"){fmt.Println("Version: 0.20.4");return nil}
 base,e:=url.Parse(os.Getenv("ALLDOWNLOADS_TEST_TDL_URL"));if e!=nil||base==nil||base.Scheme!="http"||net.ParseIP(base.Hostname())==nil||!net.ParseIP(base.Hostname()).IsLoopback(){return fmt.Errorf("isolated loopback endpoint required")}
 value:=func(flag string)string{for i,arg:=range args{if arg==flag&&i+1<len(args){return args[i+1]}};return ""}
 client:=http.Client{Timeout:2*time.Minute}
 if contains("export"){
  if !contains("--all")||!contains("--raw")||!contains("--with-content")||value("--chat")!=""{return fmt.Errorf("wrong Saved Messages export")}
  response,e:=client.Get(base.String()+"/telegram/export?range="+url.QueryEscape(value("--input")));if e!=nil{return e};defer response.Body.Close();if response.StatusCode!=200{return fmt.Errorf("export unavailable")}
  output,e:=os.Create(value("--output"));if e!=nil{return e};_,copyErr:=io.Copy(output,response.Body);return first(copyErr,output.Close())
 }
 if contains("download"){
  if contains("--group")||value("--template")!="payload"{return fmt.Errorf("download scope changed")}
  input,e:=os.ReadFile(value("--file"));if e!=nil{return e};var plan struct{Messages []struct{ID int `json:"id"`} `json:"messages"`};if json.Unmarshal(input,&plan)!=nil||len(plan.Messages)!=1{return fmt.Errorf("single frozen message required")}
  response,e:=client.Get(fmt.Sprintf("%s/telegram/file/%d",strings.TrimSuffix(base.String(),"/"),plan.Messages[0].ID));if e!=nil{return e};defer response.Body.Close();if response.StatusCode!=200{return fmt.Errorf("media unavailable")}
  output,e:=os.Create(filepath.Join(value("--dir"),"payload"));if e!=nil{return e};_,copyErr:=io.Copy(output,response.Body);return first(copyErr,output.Close())
 }
 return fmt.Errorf("unsupported isolated command")
}
func first(a,b error)error{if a!=nil{return a};return b}
