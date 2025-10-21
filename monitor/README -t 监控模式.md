# xiaobot -t （监控模式）

​	参数 -t 让xiaobot进入监控触发模式，此时xiaobot不会主动去轮询查询小爱音箱的对话，要等到收到 /monitor 给来的信号后，立即在不断静音小爱音箱的同时，开始5秒的轮询动作。得到问题后，交由AI答复，然后中止静音，由小爱TTS回答。

​	该模式不但大大减少了轮询次数，同时能最大程度的降低小爱的抢答。但因为目前还不清楚小爱播放音乐的执行方式，只能回答语音部份，要用小爱点歌、听故事的可能就不适用该模式了。

​	要实现对小爱的监控，最直接的当然是能抓取到小爱音箱的外发数据。首先想到的是让小爱走openWrt旁路由，但是折腾许久没有搞定修改小爱音箱的网关IP设置，只能作罢。目前成功实现的方案有2种：

​	1- 让小爱音箱连接我们自已用Linux搭建的wifi服务机器，就可以直接在该服务器上用tcpdump这类抓包工具获取小爱的外发数据；

​	2- 在家里的局域网内对小爱音箱做ARP欺骗(推荐使用bettercap)，让小爱音箱的外发数据流经我们自已装有抓包工具(tcpdump)的机器，从而实现监控。

# 方案1：WIFI服务器实现监控

## **A. OpenWrt下的配置**

​	将xiaobox程序解压放到/home/mi目录下。

1. **安装必要工具**：

   ```bash
   opkg update
   opkg install tcpdump
   ```

2. **配置脚本**：
   
   ```bash
   #!/bin/sh
   
   # 配置参数 - 根据实际情况修改
   TARGET_MAC="aa:bb:cc:dd:ee:ff"  # 目标设备的MAC地址（注意字母小写）
   INTERFACE="br-lan"              # 监控的网络接口，通常是br-lan
   COOLDOWN=10                     # 冷却时间(秒)，避免频繁触发
   
   echo "MAC监控脚本启动，目标MAC: $TARGET_MAC，接口: $INTERFACE"
   
   # 初始化最后触发时间
   last_trigger=0
   
   # 等待网络就绪（最多等待30秒）
   for i in {1..30}; do
     if ping -c 1 8.8.8.8 &>/dev/null; then
       break
     fi
     sleep 1
   done
   
   # 后台开启xiaobot服务
   cd /home/mi
   ./xiaobot -t > /dev/null 2>&1 &
   
   # 使用tcpdump监控外发流量
   tcpdump -i $INTERFACE -n ether src $TARGET_MAC 'and dst port 443 and (tcp[tcpflags] & tcp-syn) != 0 and tcp[13]=0x02 and dst 220.181.106.172' -v -l 2>/dev/null | while read -r line; do
       # 获取当前时间戳
       current_time=$(date +%s)
       
       # 检查是否在冷却期内
       if [ $((current_time - last_trigger)) -ge $COOLDOWN ]; then
           echo "$(date)执行调用monitor"
           
           # 调用HTTP接口
           curl -s -w " %{http_code}" "http://192.168.1.111:9997/monitor"
           
           # 更新最后触发时间
           last_trigger=$current_time
       fi
   done
   ```
   
   - 将该脚本保存为`/home/mi/mac_monitor.sh`并设置权限：`chmod +x /home/mi/mac_monitor.sh`
   - 编辑脚本中的配置参数，特别是`TARGET_MAC`（目标设备小爱音箱的MAC地址）和`INTERFACE`（网络接口，通常是`br-lan`）
   - 在执行xiaobot -t 命令后，程序会主动显示当前小爱音箱的MAC地址。 或者可以直接查看小米音箱APP的设备信息，上边也有音箱的MAC地址。
   - 这里监控机器的IP地址`192.168.1.111:9997`，需要自行修改为你监控机器的IP地址和端口。
   
3. **设置启动服务**：

   ```bash
   #!/bin/sh /etc/rc.common
   
   # 服务名称
   NAME="mac_monitor"
   # 脚本路径
   DAEMON="/home/mi/mac_monitor.sh"
   
   # 启动优先级（90表示较晚启动）
   START=90
   # 停止优先级（10表示较早停止）
   STOP=10
   
   # 使用procd进程管理
   USE_PROCD=1
   
   # 启动服务
   start_service() {
       # 检查脚本是否存在且可执行
       if [ ! -x "$DAEMON" ]; then
           echo "错误: $DAEMON 不存在或不可执行"
           return 1
       fi
       
       # 配置procd参数
       procd_open_instance
       procd_set_param command "$DAEMON"
       procd_set_param respawn  # 进程意外退出时自动重启
       procd_set_param stdout 1  # 输出重定向到系统日志
       procd_set_param stderr 1
       procd_close_instance
       
       echo "mac_monitor 服务已启动"
   }
   
   # 停止服务
   stop_service() {
       echo "正在停止 mac_monitor 服务..."
       # procd会自动处理进程停止
       return 0
   }
   
   # 重启服务
   restart_service() {
       stop
       start
   } 
   ```

   - 将该脚本保存为`/etc/init.d/mac_monitor`并设置权限：`chmod +x /etc/init.d/mac_monitor`
   - 启动服务：`/etc/init.d/mac_monitor start`
   - 设置开机自启：`/etc/init.d/mac_monitor enable`

4. **自定义触发动作**：

   - 脚本`/home/mi/mac_action.sh`作为动作脚本

   - 该脚本实现监控触发小爱的功能：
     - 用tcpdump监控记录小爱特定的外发数据。BPF过滤脚本：
     
       ether src <音箱MAC地址>
     
     - 配合`curl`调用xiaobot的/monitor这个API，发送通知给xiaobot。

## **B. Armbian(Linux)下的配置**

​	同openwrt，解压程序到/home/mi目录。

1. **安装必要工具**：

   ```bash
   apt update
   apt install tcpdump
   ```

2. **配置脚本**：

   ```bash
   #!/bin/sh
   
   # 配置参数 - 根据实际情况修改
   TARGET_MAC="aa:bb:cc:dd:ee:ff"   # 目标设备的MAC地址（注意字母小写）
   INTERFACE="eth0"	             # 监控的网络接口，通常是eth0
   COOLDOWN=10                      # 冷却时间(秒)，避免频繁触发
   
   echo "MAC监控脚本启动，目标MAC: $TARGET_MAC，接口: $INTERFACE"
   
   # 初始化最后触发时间
   last_trigger=0
   
   # 等待网络就绪（最多等待30秒）
   for i in {1..30}; do
     if ping -c 1 8.8.8.8 &>/dev/null; then
       break
     fi
     sleep 1
   done
   
   # 后台开启xiaobot服务
   cd /home/mi
   ./xiaobot -t > /dev/null 2>&1 &
   
   # 使用tcpdump监控外发流量
   tcpdump -i $INTERFACE -n ether src $TARGET_MAC 'and dst port 443 and (tcp[tcpflags] & tcp-syn) != 0 and tcp[13]=0x02 and dst 220.181.106.172' -v -l 2>/dev/null | while read -r line; do
       # 获取当前时间戳
       current_time=$(date +%s)
       
       # 检查是否在冷却期内
       if [ $((current_time - last_trigger)) -ge $COOLDOWN ]; then
           echo "$(date)执行调用monitor"
           
           # 调用HTTP接口
           curl -s -w " %{http_code}" "http://192.168.1.111:9997/monitor"
           
           # 更新最后触发时间
           last_trigger=$current_time
       fi
   done
   ```

   - 将该脚本保存为`/home/mi/mac_monitor.sh`并设置权限：`chmod +x /home/mi/mac_monitor.sh`
   - 编辑脚本中的配置参数，特别是`TARGET_MAC`（目标设备小爱音箱的MAC地址）和`INTERFACE`（网络接口，通常是`eth0`）
   - 在执行xiaobot -t 命令后，程序会主动显示当前小爱音箱的MAC地址。 或者可以直接查看小米音箱APP的设备信息，上边也有音箱的MAC地址。
   - 这里监控机器的IP地址`192.168.1.111:9997`，需要自行修改为你监控机器的IP地址和端口。

3. **设置启动服务**：

   在Armbian系统中，默认情况下*rc.local*脚本不会自动启动。要启用开机启动脚本，可以按照以下步骤操作：

   修改rc.local.service文件

   首先，使用*vi*或*winscp*编辑*/lib/systemd/system/rc.local.service*文件，并在文件末尾添加以下内容：

   ```
   [Install]
   WantedBy=multi-user.target
   Alias=rc-local.service
   ```

   注：在有些Armbian系统中，可能不是rc.local.service文件，而是rc-local.service文件。

   启用rc-local.service服务

   接下来，启用并启动*rc-local.service*服务：

   ```
   sudo systemctl enable rc-local
   sudo systemctl start rc-local.service
   ```

   添加开机启动脚本

   在**/etc/rc.local**文件中添加需要的开机启动脚本，确保脚本写在 **exit 0** 之前。例如：

   ```
   #!/bin/bash -e
   # rc.local
   # 在此处添加您的启动脚本
   bash /home/mi/mac_monitor.sh
   
   exit 0
   ```
   通过以上步骤，可以在Armbian、Ubuntu 等系统中成功配置开机启动脚本。

4. **其他方法**

   除了使用 systemd，还可以通过编辑 */etc/init.d* 目录下的脚本来实现开机自启动。首先在/etc/init.d目录下创建一个名为 *xiaobot* 的脚本：

   vim /etc/init.d/xiaobot

   输入以下内容：

   ```
   #!/bin/sh
   
   ### BEGIN INIT INFO
   # Provides: xiaobot
   # Required-Start: $network $remote_fs $syslog
   # Required-Stop: $network $remote_fs $syslog
   # Default-Start: 2 3 4 5
   # Default-Stop: 0 1 6
   # Short-Description: xiaobot script at startup
   # Description: xiaobot service on startup
   ### END INIT INFO
   
   bash /home/mi/mac_monitor.sh
   
   exit 0
   ```

   使脚本可执行并配置为开机自动运行：

   ```
   sudo chmod +x /etc/init.d/xiaobot
   sudo update-rc.d xiaobot defaults
   ```
   
   这样，每次开机时，系统会自动执行该脚本。
   
   可运行以下命令以启动服务：
   
   ```
   sudo service xiaobot start
   ```
   
   


# 方案2：ARP欺骗实现监控

1. **先禁用局域网内网关的【局域网防火墙】**

   局域网防火墙若开启会即时发现arp、dhcp欺骗并给拦截掉，后边的ARP欺骗就起不了作用。

2. **安装必要工具**：

   ```bash
   apt update
   apt install tcpdump bettercap
   ```

3. **启用监控主机数据包转发**

   要在你的监控主机上运行ARP欺骗程序，先要打开监控主机的数据包转发服务，否则小爱音箱就连不了外网了。执行：

   ```vim /etc/sysctl.conf```

   在这里可以增加一条数据： net.ipv4.ip_forward = 1

   如果已有net.ipv4.ip_forward转发项且已被设为0那么你只需要将它改为1即可。

   也可以直接使用命令`sysctl -w net.ipv4.ip_forward=1`来设置。

   要想让命令更改即时生效，可执行以下指指令：

   sysctl -p /etc/sysctl.conf

   通过重启网络服务使之生效。

4. **建立ARP欺骗脚本**

   这里使用 bettercap，先建立开启arp欺骗的执行脚本文件arpspoof.cap，内容如下：

   ````
   net.probe on
   sleep 5
   set arp.spoof.targets <小爱音箱MAC地址>
   arp.spoof on
   net.sniff on
   ````

   - 将该脚本保存为`/home/mi/arpspoof.cap`。

   - 要知道小爱音箱的MAC地址，在执行xiaobot -t 命令后，程序会主动显示当前小爱音箱的MAC地址。 或者可以直接查看小米音箱APP的设备信息，上边也有音箱的MAC地址。

   可执行以下指令开启ARP欺骗：

   ```
   bettercap -caplet /home/mi/arpspoof.cap
   ```

5. **建立监控脚本**

   配置/hom/mi/mac_monitor.sh脚本：

   ```bash
   #!/bin/sh
   
   # 配置参数 - 根据实际情况修改
   TARGET_MAC="aa:bb:cc:dd:ee:ff"   # 目标设备的MAC地址（注意字母小写）
   INTERFACE="eth0"	             # 监控的网络接口，通常是eth0
   COOLDOWN=10                      # 冷却时间(秒)，避免频繁触发
   
   echo "MAC监控脚本启动，目标MAC: $TARGET_MAC，接口: $INTERFACE"
   
   # 初始化最后触发时间
   last_trigger=0
   
   # 使用tcpdump监控外发流量
   tcpdump -i $INTERFACE -n ether src $TARGET_MAC 'and dst port 443 and (tcp[tcpflags] & tcp-syn) != 0 and tcp[13]=0x02 and (dst 220.181.106.172 or dst 106.120.178.12)' -v -l 2>/dev/null | while read -r line; do
       # 获取当前时间戳
       current_time=$(date +%s)
       
       # 检查是否在冷却期内
       if [ $((current_time - last_trigger)) -ge $COOLDOWN ]; then
           echo "$(date)执行调用monitor"
           
           # 调用HTTP接口
           curl -s -w " %{http_code}" "http://192.168.1.111:9997/monitor"
           
           # 更新最后触发时间
           last_trigger=$current_time
       fi
   done
   ```

   - 将该脚本保存为`/home/mi/mac_monitor.sh`并设置权限：`chmod +x /home/mi/mac_monitor.sh`
   - 编辑脚本中的配置参数，特别是`TARGET_MAC`（目标设备小爱音箱的MAC地址）和`INTERFACE`（网络接口，通常是`eth0`）
   - 在执行xiaobot -t 命令后，程序会主动显示当前小爱音箱的MAC地址。 或者可以直接查看小米音箱APP的设备信息，上边也有音箱的MAC地址。
   - 这里监控机器的IP地址`192.168.1.111:9997`，需要自行修改为你监控机器的IP地址和端口。

   开启ARP欺骗后，即可在本监控主机上运行`mac_monitor.sh`脚本程序，实现监控。

6. **添加开机启动脚本**

   在/etc/rc.local文件中添加需要的开机启动脚本，确保脚本写在 **exit 0** 之前。例如：

   ```
   #!/bin/bash -e
   # rc.local
   # 在此处添加您的启动脚本
   
   # 等待网络就绪（最多等待30秒）
   for i in {1..30}; do
     if ping -c 1 8.8.8.8 &>/dev/null; then
       break
     fi
     sleep 1
   done
   
   # 执行xiaobot
   cd /home/mi
   bettercap -no-history -silent -caplet ./arpspoof.cap > /dev/null 2>&1 &
   bash ./mac_monitor.sh > /dev/null 2>&1 &
   ./xiaobot -t > /dev/null 2>&1 &
   
   exit 0
   ```

7. **设置启动服务**

   （可以直接参考**方案1**中Linux部份的**设置启动服务**内容，将`mac_monitor.sh`的地方替换成 `/home/mi/autorun.sh` 即可。）

   修改rc.local.service文件，启用开机启动脚本：

   首先，使用*vi*或*winscp*编辑*/lib/systemd/system/rc.local.service*文件，并在文件末尾添加以下内容：

   ```
   [Install]
   WantedBy=multi-user.target
   Alias=rc-local.service
   ```

   注：在有些Armbian系统中，可能不是rc.local.service文件，而是rc-local.service文件。

   启用rc-local.service服务

   接下来，启用并启动*rc-local.service*服务：

   ```
   sudo systemctl enable rc-local.service
   sudo systemctl start rc-local.service
   ```
   查看服务状态：
   ```
   systemctl status rc-local.service
   ```
   重启服务：
   ```
   systemctl restart rc-local.service
   ```

   通过以上步骤，完成在监控主机上运行xiaobot。若xiaobot没有正常工作，可检查 `rc-local` 的日志，确认具体哪个命令失败：

   ```bash
   journalctl -u rc-local.service -b  # 查看本次启动的日志
   ```

