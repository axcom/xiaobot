package shell

import (
	"fmt"
	"ninego/log"
	"os"
	"os/exec"
	"strings"

	"github.com/dop251/goja"
	//"bytes"
	//"io/ioutil"
	//"golang.org/x/text/encoding/simplifiedchinese"
	//"golang.org/x/text/transform"
)

// 处理路径，确保末尾反斜杠后有空格或被正确转义
func fixPath(cmdstr string) string {
	// 针对dir命令处理路径末尾的反斜杠
	if strings.HasPrefix(strings.ToLower(cmdstr), "dir ") {
		parts := strings.SplitN(cmdstr, " ", 2)
		if len(parts) == 2 {
			path := parts[1]
			// 如果路径以\结尾且后面没有其他字符，添加空格
			if strings.HasSuffix(path, "\\") && !strings.HasSuffix(path, "\\ ") {
				return parts[0] + " " + path + " "
			}
		}
	}
	return cmdstr
}

//cmdrun 执行命令，支持管道 (|) 和输出重定向 (>) 操作// 通过调用系统 shell 来解析这些特殊操作符
func shell(cmdstr string) error {
	var cmd *exec.Cmd
	log.Println("shell>", cmdstr)
	// 根据操作系统选择合适的 shell
	if os.PathSeparator == '\\' {
		// Windows 系统使用 cmd.exe
		cmd = exec.Command("cmd.exe", "/c", fixPath(cmdstr))
	} else {
		// 类 Unix 系统 (linux, macOS 等) 使用 /bin/sh
		cmd = exec.Command("/bin/sh", "-c", cmdstr)
	}
	// 执行命令并获取合并的输出 (标准输出 + 标准错误)
	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))
	if err != nil {
		log.Println("命令执行失败:", err)
		return err
	}
	return nil
}

/*/ 将GBK编码转换为UTF-8
func gbkToUtf8(input []byte) ([]byte, error) {
	// 使用GBK解码器创建转换读取器
	reader := transform.NewReader(bytes.NewReader(input), simplifiedchinese.GBK.NewDecoder())
	// 读取转换后的数据
	return ioutil.ReadAll(reader)
}

// windows中文输出转换正常
func cmd(cmdstr string) error {
	var cmd *exec.Cmd
	var output []byte
	var err error

	if os.PathSeparator == '\\' {
		// Windows系统处理
		cmd = exec.Command("cmd.exe", "/c", fixPath(cmdstr))
		output, err = cmd.CombinedOutput()

		// 尝试将GBK转换为UTF-8
		if err == nil {
			converted, convErr := gbkToUtf8(output)
			if convErr == nil {
				output = converted
			} else {
				log.Println("GBK转UTF-8失败，使用原始输出:", convErr)
			}
		}
	} else {
		// 类Unix系统直接执行
		cmd = exec.Command("/bin/sh", "-c", cmdstr)
		output, err = cmd.CombinedOutput()
	}

	// 输出处理后的结果
	fmt.Printf("命令: %s\n", cmdstr)
	fmt.Printf("输出:\n%s\n", string(output))

	if err != nil {
		log.Printf("命令执行失败: %s\n", err)
		return err
	}
	return nil
}*/

// 单条命令执行
func command(cmdstr string) error {
	log.Println(cmdstr)
	args := strings.Split(fixPath(cmdstr), " ")
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))
	if err != nil {
		log.Println("command() failed with ", err)
	}
	return err
}

func Enable(runtime *goja.Runtime) error {
	if err := runtime.Set("command", command); err != nil {
		return err
	}
	return runtime.Set("shell", shell)
}
