# 常见密码表来源与许可

- 文件：`10k-most-common.txt`
- 来源：SecLists `Passwords/Common-Credentials/10k-most-common.txt`
- 固定提交：`d9458f277ed978a608ad165e5eb2fdb389e4c7ee`
- 下载地址：<https://raw.githubusercontent.com/danielmiessler/SecLists/d9458f277ed978a608ad165e5eb2fdb389e4c7ee/Passwords/Common-Credentials/10k-most-common.txt>
- 文件 SHA-256：`68782d6a4a19a4768d5f15dd66bd534e7a33055cc755411e33f16d18c50fdcce`
- 字节数：73026；行数：10001（最后一行是换行符结尾，加载时跳过空行）
- 许可：SecLists 以 MIT 许可发布，见 <https://github.com/danielmiessler/SecLists/blob/master/LICENSE>（MIT License, Copyright (c) 2018 Daniel Miessler）

该文件随应用发布，运行时不访问外部服务。校验规则只做完整匹配：比较前把输入密码的 ASCII 字母转小写，不做子串或前缀匹配。
