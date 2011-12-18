package httprpc

import (
  "io/ioutil"
  "os"
  "json"
  "strings"
  "http"
  "log"
  "reflect"
  "sync"
  "utf8"
  "unicode"
)

type Request struct {
  Method string
}

type Request2 struct {
  Params interface{}
  Id     string
}

type ErrorResponse struct {
  Error string
}

type methodType struct {
  sync.Mutex // protects counters
  method     reflect.Method
  ArgType    reflect.Type
  ReplyType  reflect.Type
  numCalls   uint
}

type service struct {
  name   string                 // name of service
  rcvr   reflect.Value          // receiver of methods for the service
  typ    reflect.Type           // type of the receiver
  method map[string]*methodType // registered methods
}

type Server struct {
  serviceMap map[string]*service
}

func NewServer() *Server {
  return &Server{
    serviceMap: make(map[string]*service),
  }
}

var unusedError *os.Error
var typeOfOsError = reflect.TypeOf(unusedError).Elem()

// Is this an exported - upper case - name?
func isExported(name string) bool {
  rune, _ := utf8.DecodeRuneInString(name)
  return unicode.IsUpper(rune)
}

// Is this type exported or a builtin?
func isExportedOrBuiltinType(t reflect.Type) bool {
  for t.Kind() == reflect.Ptr {
    t = t.Elem()
  }
  // PkgPath will be non-empty even for an exported type,
  // so we need to check the type name as well.
  return isExported(t.Name()) || t.PkgPath() == ""
}

func (server *Server) Register(impl interface{}) {
  s := &service{}
  s.typ = reflect.TypeOf(impl)
  s.rcvr = reflect.ValueOf(impl)
  s.name = reflect.Indirect(s.rcvr).Type().Name()
  s.method = make(map[string]*methodType)

  for m := 0; m < s.typ.NumMethod(); m++ {
    method := s.typ.Method(m)
    mtype := method.Type
    mname := method.Name
    if method.PkgPath != "" {
      continue
    }
    // Method needs three ins: receiver, *args, *reply.
    if mtype.NumIn() != 3 {
      log.Println("method", mname, "has wrong number of ins:", mtype.NumIn())
      continue
    }
    // First arg must be a pointer.
    argType := mtype.In(1)
    if argType.Kind() != reflect.Ptr {
      log.Println(mname, "argument type not a pointer:", argType)
      continue
    }
    if !isExportedOrBuiltinType(argType) {
      log.Println(mname, "argument type not exported or local:", argType)
      continue
    }
    // Second arg must be a pointer.
    replyType := mtype.In(2)
    if replyType.Kind() != reflect.Ptr {
      log.Println("method", mname, "reply type not a pointer:", replyType)
      continue
    }
    if !isExportedOrBuiltinType(replyType) {
      log.Println("method", mname, "reply type not exported or local:", replyType)
      continue
    }
    // Method needs one out: os.Error.
    if mtype.NumOut() != 1 {
      log.Println("method", mname, "has wrong number of outs:", mtype.NumOut())
      continue
    }
    if returnType := mtype.Out(0); returnType != typeOfOsError {
      log.Println("method", mname, "returns", returnType.String(), "not os.Error")
      continue
    }
    s.method[mname] = &methodType{method: method, ArgType: argType, ReplyType: replyType}
  }
  server.serviceMap[s.name] = s
  log.Printf("%#v", s)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "application/json")
  w.Header().Set("Access-Control-Allow-Origin", "*")
  w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
  w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

  log.Printf("----------------\n")
  req := &Request{}
  data, err := ioutil.ReadAll(r.Body)
  if err != nil {
    log.Println(err.String())
    return
  }
  json.Unmarshal(data, req)
  log.Printf("Request: %#v", req)
  // Find the receiver object.
  sname := strings.Split(req.Method, ".")
  service, ok := s.serviceMap[sname[0]]
  if !ok || len(sname) != 2 {
    log.Println("No such service")
    return
  }
  // Find the method.
  method, ok := service.method[sname[1]]
  if !ok {
    log.Println("No such method")
    return
  }
  function := method.method.Func

  // Prepare params.
  argv := reflect.New(method.ArgType.Elem())
  req2 := &Request2{Params: argv.Internal}
  // Parse params (again, could be improved...)
  json.Unmarshal(data, req2)

  // Prepare reply object.
  replyv := reflect.New(method.ReplyType.Elem())
  // Call the function
  returnVals := function.Call([]reflect.Value{service.rcvr, argv, replyv})

  // Check for error returned.
  errInter := returnVals[0].Interface()
  errmsg := ""
  out := make([]byte, 0, 0)
  if errInter != nil {
    errmsg = errInter.(os.Error).String()
    out, _ = json.Marshal(ErrorResponse{errmsg})
  } else {
    out, _ = json.Marshal(replyv.Internal)
  }
  // Write output.
  log.Printf(string(out))
  w.Write(out)
}
