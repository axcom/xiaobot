package webui

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ninego/log"
	"xiaobot"
	"xiaobot/jsengine"
	"xiaobot/miservice"
)

//go:embed website/*
var Assets embed.FS

var submit = false

type Chat struct {
	Message    string `json:"message"`
	PlayQuery  bool   `json:"playQuery"`
	PlayAnswer bool   `json:"playAnswer"`
	Stream     bool   `json:"stream"`
}

//在同一次请求响应过程中，只能调用一次WriteHeader(code int)，否则会有一条日志输出“http: superfluous response.WriteHeader call from”。
type ResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *ResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	w.wroteHeader = true
}

func do_Task(writer http.ResponseWriter, request *http.Request) {
	act := request.PathValue("action")
	jsScript := config.TaskJS[act]
	log.Println("execute action:", act)
	rw := &ResponseWriter{ResponseWriter: writer}
	if err := jsengine.Do_Action(rw, request, &jsScript); err == nil {
		if !rw.wroteHeader {
			writer.WriteHeader(http.StatusOK)
		}
	}
}

func do_Chat(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Body == nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if bot != nil {
		var chat Chat
		/*body, _ := io.ReadAll(request.Body)
		fmt.Println("Received body:", string(body))
		request.Body = io.NopCloser(bytes.NewBuffer(body)) // 注意：读取后需重置 r.Body 才能继续解析		*/
		err := json.NewDecoder(request.Body).Decode(&chat)
		if err != nil {
			log.Error("Error:", err)
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		log.Debug("body->chat", chat)
		chatMode := 0
		if chat.PlayQuery && chat.PlayAnswer {
			chatMode = 3
		} else if chat.PlayQuery {
			chatMode = 1
		} else if chat.PlayAnswer {
			chatMode = 2
		}
		log.Println("chatmode=", chatMode)
		if (chatMode == 0 || chat.Stream) && config.Stream && bot.HasGPT {
			// 流式响应处理
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.Header().Set("Cache-Control", "no-cache")
			writer.Header().Set("Connection", "keep-alive")
			writer.Header().Set("Access-Control-Allow-Origin", "*") // 跨域支持
			err := bot.ChatStream(chat.Message, writer)
			if err != nil {
				//http.Error 在ChatStream已调用过了
				log.Error("stream chat error:", err)
			}
		} else {
			// 普通响应处理
			bot.Talk.StreamWriter = writer
			answer := bot.Chat(chat.Message, chatMode)
			resp := struct {
				//Code    int    `json:"code"`
				Message string `json:"message"`
			}{
				//Code:    100,
				Message: answer,
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			json.NewEncoder(writer).Encode(resp)
		}
	} else {
		//writer.WriteHeader(http.StatusOK)
		writer.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(writer).Encode(map[string]string{"error": "bot is not initialized"})
	}
}

func do_Monitoring(writer http.ResponseWriter, request *http.Request) {
	if bot != nil && ticker != nil {
		ticker.Reset(monitoringTriggerInterval)
		bot.UpdateMonitor(+1)
	}
	writer.WriteHeader(http.StatusOK)
}

func do_setConfig(writer http.ResponseWriter, request *http.Request) {
	log.Debug("config submit", request.Method, request.Body)
	if request.Method != http.MethodPost || request.Body == nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if submit {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	log.Debug("body->config", json.NewDecoder(request.Body))
	err := json.NewDecoder(request.Body).Decode(&config)
	if err != nil {
		log.Error("Error:", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	log.Debug("", config)
	// 简单的验证
	if config.Account == "" {
		http.Error(writer, "missing xiaomi account field", http.StatusBadRequest)
		return
	}
	// 保存配置到文件
	err = saveConfig(config)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	log.Debug("Received config:", config)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	s := "配置写入config.json完成，请运行 'xiaobot' 启动bot服务。"
	writer.Write([]byte(`{"message":"` + s + `"}`))
	log.Println("配置写入config.json")

	// 通知主程序关闭完成
	WebDone <- true
}

// 保存配置到文件
func saveConfig(config *xiaobot.Config) error {
	// 将配置转换为JSON
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 确定配置文件路径
	configPath := xiaobot.ConfigFile
	if configPath == "" {
		configPath = filepath.Join("config.json")
	}

	// 写入文件
	return ioutil.WriteFile(configPath, data, 0600)
}

//获取配置的处理函数
func do_getConfig(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 确定配置文件路径
	// 读取已保存的配置文件
	configPath := xiaobot.ConfigFile
	if configPath == "" {
		configPath = filepath.Join("config.json")
	}
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		// 如果文件不存在，返回空配置
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte("{}"))
		return
	}

	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Error("Error parsing config:", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	json.NewEncoder(writer).Encode(config)
}

type device struct {
	Name     string `json:"name"`
	MiotDID  string `json:"midid"`
	Hardware string `json:"hardware"`
}
type account struct {
	UserID   string `json:"account"`
	Password string `json:"password"`
}

func do_getDeviceList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	miuser := account{}
	err := json.NewDecoder(request.Body).Decode(&miuser)
	if err != nil {
		log.Error("Error decoding request body:", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	account := miservice.NewAccount(
		miuser.UserID,
		miuser.Password,
		miservice.NewTokenStore(filepath.Join(os.Getenv("HOME"), ".mi.token")),
	)
	service := miservice.NewAIService(account)
	deviceList, err := service.DeviceList(0)
	if err != nil {
		log.Error("Error getting device list:", err)
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	var devices []device
	for i := 0; i < len(deviceList); i++ {
		// 查重
		finded := false
		for _, data := range devices {
			if data.MiotDID == deviceList[i].MiotDID {
				finded = true
				break
			}
		}
		if finded {
			continue
		}
		data := device{
			Name:     deviceList[i].Alias,
			MiotDID:  deviceList[i].MiotDID,
			Hardware: deviceList[i].Hardware,
		}

		// 是否支持
		finded = false
		for k, _ := range xiaobot.HardwareCommandDict {
			if k == data.Hardware {
				finded = true
				break
			}
		}
		if !finded {
			data.Hardware += "(暂不支持)"
		}

		devices = append(devices, data)
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	// 直接返回设备列表，不进行二次编码
	json.NewEncoder(writer).Encode(devices)
}

func Router() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/task/{action}", do_Task)
	mux.HandleFunc("/chat", do_Chat)
	mux.HandleFunc("/monitor", do_Monitoring)
	mux.HandleFunc("/submit-config", do_setConfig)
	mux.HandleFunc("/get-config", do_getConfig)
	mux.HandleFunc("/get-devices", do_getDeviceList)

	// 创建一个子文件系统，只包含static目录下的内容
	staticFS, _ := fs.Sub(Assets, "website")
	// 将静态文件系统注册到HTTP服务器
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	// 添加一个处理器，将/config路径映射到static/config.html
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		// 从嵌入的文件系统读取static/config.html
		content, _ := Assets.ReadFile("website/config.html")
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
	//提问内容js编辑
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		// 从嵌入的文件系统读取static/query_jseditor.html
		content, _ := Assets.ReadFile("website/query_jseditor.html")
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
	mux.HandleFunc("/query/script", func(w http.ResponseWriter, r *http.Request) {
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(config.QueryJS))
	})
	mux.HandleFunc("/query/test", do_queryTest)
	mux.HandleFunc("/query/save", do_querySave)

	//任务内容js编辑
	mux.HandleFunc("/task", func(w http.ResponseWriter, r *http.Request) {
		// 从嵌入的文件系统读取html
		content, _ := Assets.ReadFile("website/tasks_jseditor.html")
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
	mux.HandleFunc("/task/list", func(w http.ResponseWriter, r *http.Request) {
		tasks := make([]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Memo string `json:"memo"`
		}, len(config.TaskJS))
		// 排序
		var keys []string
		for k := range config.TaskJS {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// 生成Slice
		for i := 0; i < len(keys); i++ {
			jsname := keys[i]
			tasks[i].ID = jsname
			tasks[i].Name = jsname
			tasks[i].Memo = strings.Split(config.TaskJS[jsname], "\n")[0]
		}
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tasks)
	})
	mux.HandleFunc("/task/script/{action}", func(w http.ResponseWriter, r *http.Request) {
		taskName := r.PathValue("action")
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(config.TaskJS[taskName]))
			return
		}
		if r.Method != http.MethodPost || r.Body == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// 更安全的读取方式
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Error("Read body failed:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close() // 确保关闭body
		err = ioutil.WriteFile(taskName+".bot", body, 0666)
		if err != nil {
			log.Error("Write file failed:", taskName, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Println("Write file", taskName, "- ok")
		config.TaskJS[taskName] = string(body)
		// 设置正确的Content-Type
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)

	})
	mux.HandleFunc("/task/test/{action}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// 更安全的读取方式
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Error("Read body failed:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close() // 确保关闭body
		jscode := string(body)
		rw := &ResponseWriter{ResponseWriter: w}
		if err := jsengine.Do_Action(rw, r, &jscode); err == nil {
			log.Println("task", r.PathValue("action"), "test - ok")
			if !rw.wroteHeader {
				// 设置正确的Content-Type
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusOK)
			}
		}
	})

	return mux
}

func do_queryTest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Body == nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	req := struct {
		Question      string `json:"question"`
		ScriptContent string `json:"scriptContent"`
	}{}
	err := json.NewDecoder(request.Body).Decode(&req)
	if err != nil {
		log.Error("request Error:", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := jsengine.Exec_queryJS(req.Question, req.ScriptContent); err != nil {
		log.Error("Execute query JavaScript Error:", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	log.Println("test 'query.bot' - ok!")
	// 设置正确的Content-Type
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	writer.Write([]byte("success"))
}
func do_querySave(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Body == nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	// 更安全的读取方式
	body, err := io.ReadAll(request.Body)
	if err != nil {
		log.Error("Read body failed:", err)
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	defer request.Body.Close() // 确保关闭body
	err = ioutil.WriteFile("query.bot", body, 0666)
	if err != nil {
		log.Error("Write file 'query.bot' failed:", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	log.Println("Write file 'query.bot' - ok")
	config.QueryJS = string(body)
	// 设置正确的Content-Type
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
}
