package xiaobot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ninego/log"
	"xiaobot/miservice"
)

// 常量定义集中管理
const (
	MusicIndex  = 4
	StateIndex  = 3
	WakeupIndex = 2
	ActionIndex = 1
	TTSIndex    = 0
)

type XiaoMi struct {
	config *Config
	token  miservice.TokenStore

	deviceID string
	MacAddr  string //音箱MAC地址

	account     *miservice.Account
	minaService *miservice.AIService
	miioService *miservice.IOService
}

func NewXiaoMi(config *Config) *XiaoMi {
	tokens := miservice.NewTokenStore(config.TokenPath)
	return &XiaoMi{
		config: config,
		token:  tokens,
	}
}

type Record struct {
	BitSet  []int `json:"bitSet"`
	Answers []struct {
		BitSet []int  `json:"bitSet"`
		Type   string `json:"type"`
		Tts    struct {
			BitSet []int  `json:"bitSet"`
			Text   string `json:"text"`
		} `json:"tts"`
		/*Audio struct {
			BitSet        []int `json:"bitSet"`
			AudioInfoList []struct {
				BitSet []int  `json:"bitSet"`
				Title  string `json:"title"`
				Artist string `json:"artist"`
				CpName string `json:"cpName"`
			} `json:"audioInfoList"`
		} `json:"audio"`*/
	} `json:"answers"`
	Time      int64  `json:"time"`
	Query     string `json:"query"`
	RequestID string `json:"requestId"`
}

type Records struct {
	BitSet      []int    `json:"bitSet"`
	Records     []Record `json:"records"`
	NextEndTime int64    `json:"nextEndTime"`
}

type requestRet struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Data    string `json:"data,omitempty"`
}

func (mt *XiaoMi) getLatestAsk() (*Records, error) {
	retries := 2
	for i := 0; i < retries; i++ {
		u := strings.Replace(LatestAskApi, "{hardware}", mt.config.Hardware, -1)
		u = strings.Replace(u, "{timestamp}", strconv.FormatInt(time.Now().Unix()*1000, 10), -1)
		var result requestRet
		err := mt.account.Request(MicoApi, u, nil, func(tokens *miservice.Tokens, cookie map[string]string) url.Values {
			cookie["deviceId"] = mt.deviceID
			return nil
		}, nil, true, &result)
		if err != nil {
			log.Error("get latest ask from xiaoai error, retry", err)
			continue
		}

		var records Records
		if err := json.Unmarshal([]byte(result.Data), &records); err != nil {
			log.Error("get latest ask from xiaoai error", err)
			continue
		}
		return &records, nil
	}

	return nil, errors.New("max retries exceeded")
}

func (mt *XiaoMi) InitMiBox() error {
	if err := mt.loginXiaoMi(); err != nil {
		return fmt.Errorf("登录失败: %w", err)
	}

	if err := mt.initDataHardware(); err != nil {
		return fmt.Errorf("硬件初始化失败: %w", err)
	}

	return nil
}

func (mt *XiaoMi) loginXiaoMi() error {
	account := miservice.NewAccount(
		mt.config.Account,
		mt.config.Password,
		mt.token,
	)
	mt.account = account
	if err := account.Login(MicoApi); err != nil {
		return err
	}
	mt.minaService = miservice.NewAIService(account)
	mt.miioService = miservice.NewIOService(account, nil)
	return nil
}

func (mt *XiaoMi) initDataHardware() error {
	if mt.config.MiDID == "" {
		devices, err := mt.miioService.DeviceList(false, 0)
		if err != nil {
			return err
		}
		found := false
		for _, d := range devices {
			if strings.HasSuffix(d.Model, strings.ToLower(mt.config.Hardware)) {
				mt.config.MiDID = d.Did
				found = true
				break
			}
		}
		if !found {
			return errors.New("cannot find did for hardware: " + mt.config.Hardware + " please set it via MI_DID env")
		}
	}

	hardwareData, err := mt.minaService.DeviceList(0)
	if err != nil {
		return err
	}
	for _, h := range hardwareData {
		if h.MiotDID == mt.config.MiDID {
			mt.deviceID = h.DeviceID
			mt.MacAddr = normalizeMAC(h.Mac)
			break
		}
	}
	if mt.deviceID == "" {
		for _, h := range hardwareData {
			if h.Hardware == mt.config.Hardware {
				mt.deviceID = h.DeviceID
				mt.MacAddr = normalizeMAC(h.Mac)
				break
			}
		}
	}
	if mt.deviceID == "" {
		return errors.New("we have no hardware: " + mt.config.Hardware + " please use micli mina to check")
	}

	return nil
}

type StatusInfo struct {
	Status   int `json:"status"`
	Volume   int `json:"volume"`
	LoopType int `json:"loop_type"`
}

/*func (mt *XiaoMi) speakerIsPlaying() (bool, error) {
	res, err := mt.minaService.PlayerGetStatus(mt.deviceID)
	if err != nil {
		return false, err
	}
	var info StatusInfo
	json.Unmarshal([]byte(res.Data.Info), &info)
	return info.Status == 1, nil
}*/
func (mt *XiaoMi) speakerIsPlaying() (int, error) {
	if mt.config.UseCommand {
		w := mt.hardwareCommand(StateIndex)
		state, err := mt.miioService.MiotGetProp(mt.config.MiDID, propId(w))
		if err != nil {
			return 2, fmt.Errorf("获取播放状态失败: %w", err)
		}
		return state.(int), nil
	} else {
		res, err := mt.minaService.PlayerGetStatus(mt.deviceID)
		if err != nil {
			return 2, fmt.Errorf("获取播放状态失败: %w", err)
		}
		var info StatusInfo
		if err := json.Unmarshal([]byte(res.Data.Info), &info); err != nil {
			return 2, fmt.Errorf("解析播放状态失败: %w", err)
		}
		return info.Status, nil
	}
}

func (mt *XiaoMi) stopSpeaker() error {
	_, err := mt.minaService.PlayerPause(mt.deviceID)
	return err
}

func (mt *XiaoMi) hardwareCommand(index int) string {
	v, ok := HardwareCommandDict[mt.config.Hardware]
	if !ok {
		v = DefaultCommand
	}
	return v[index]
}

func propId(action string) miservice.Iid {
	ids := strings.Split(action, ",")
	if len(ids) < 2 {
		return miservice.Iid{0, 0}
	}
	siid, _ := strconv.Atoi(ids[0])
	iid, _ := strconv.Atoi(ids[1])
	return miservice.Iid{siid, iid}
}

func actionId(action string) []int {
	ids := strings.Split(action, "-")
	if len(ids) < 2 {
		return []int{0, 0}
	}
	siid, _ := strconv.Atoi(ids[0])
	iid, _ := strconv.Atoi(ids[1])
	return []int{siid, iid}
}

func (mt *XiaoMi) wakeUp() {
	if _, err := mt.miioService.MiotAction(mt.config.MiDID, actionId(mt.hardwareCommand(WakeupIndex)), nil); err == nil {
		return
	}

	w := mt.hardwareCommand(ActionIndex)
	if _, err := mt.miioService.MiotAction(mt.config.MiDID /*.deviceID*/, actionId(w), []interface{}{WakeupKeyword, 0}); err != nil {
		log.Error("唤醒设备失败: ", err)
	}
}

// miTTS 文本转语音，优化分片处理
func (mt *XiaoMi) miTTS(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if mt.config.UseCommand {
		t := mt.hardwareCommand(TTSIndex)
		c, err := mt.miioService.MiotAction(mt.config.MiDID /*.deviceID*/, actionId(t), []interface{}{message})
		log.Debug("TTS command result", c, err)
		if err != nil {
			return fmt.Errorf("TTS命令执行失败: %w", err)
		}
	} else {
		if _, err := mt.minaService.TextToSpeech(mt.deviceID, message); err != nil {
			if err != nil {
				log.Error("Error: ", err)
				return fmt.Errorf("TTS服务调用失败: %w", err)
			}
		}
	}
	return nil
}

func (mt *XiaoMi) waitForTTSFinish() {
	/*for {
		isPlaying, _ := mt.speakerIsPlaying()
		if !isPlaying {
			break
		}
		time.Sleep(1 * time.Second)
	}*/
	for {
		statusPlaying, err := mt.speakerIsPlaying()
		if err != nil || statusPlaying != 1 {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func (mt *XiaoMi) miPlay(url string) error {
	m := mt.hardwareCommand(MusicIndex)
	if m != "" {
		v, err := mt.minaService.PlayByMusicUrl(mt.deviceID, url)
		if err != nil {
			log.Error(fmt.Sprintf("play music url %v, Error: %v\n", v, err))
			return err
		}
	} else {
		v, err := mt.minaService.PlayByUrl(mt.deviceID, url)
		if err != nil {
			log.Error(fmt.Sprintf("play url %v, Error: %v\n", v, err))
			return err
		}
	}
	return nil
}

func (mt *XiaoMi) miAction(message string) error {
	_, err := mt.miioService.MiotAction(mt.config.MiDID, actionId(mt.hardwareCommand(ActionIndex)), []interface{}{message, 0})
	return err
}
