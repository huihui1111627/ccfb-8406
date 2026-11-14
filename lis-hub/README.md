# lis-hub 检验数据集成平台

面向第三方检验所的数据集成平台: 接收各仪器/合作医院的检验结果(HL7 v2 / ASTM / Excel),
统一标准化后入库, 提供网页控制台。Go + SQLite, 单二进制部署。

## 功能

| 模块 | 说明 |
|---|---|
| 接口接入 | HTTP POST、MLLP/TCP 监听(自动识别 HL7/ASTM)、Excel 模板导入; 容错解析: GBK 乱码自动转码、截断报文抢救已接收部分、校验和错误告警不丢数据 |
| 项目映射 | 各来源项目代码/单位 → 标准项目编码, 未映射项目自动登记, 控制台可维护; 支持单位换算系数 |
| 结果查询 | 按样本号/病人/项目过滤; 参考范围按性别+年龄匹配, 自动标 H/L |
| 去重与更正 | 报文级 hash 去重 + 结果级 dedup key 去重; 更正结果(C状态)挂版本链, 历史版本可溯 |
| 危急值 | 按项目/性别/年龄配置规则, 实时触发事件; open → 确认 → 关闭 闭环, 全程留痕 |
| 质控 | 质控批靶值/SD 维护, Westgard 多规则(1_2s/1_3s/2_2s/R_4s/4_1s/10x), Levey-Jennings 图 |
| 溯源 | 任何结果可追回原始报文, 控制台"对照"视图左右并排看原始报文与标准化结果 |
| 统计 | 按仪器/项目统计 TAT(平均/P50/P90/最大) |

## 运行

```bash
go build -o lis-hub ./cmd/server
./lis-hub -db lis.db -http :18080 -tcp :16600
# 控制台: http://localhost:18080
```

- `-http` 网页控制台与 REST API
- `-tcp` MLLP(HL7)/ASTM 接入监听, 留空关闭
- 首次启动自动写入演示主数据(标准项目、参考范围、危急值规则、映射、质控批), `-seed=false` 关闭

## 测试报文

```bash
go run ./cmd/gen ./testdata   # 生成到 testdata/
```

覆盖场景: 正常 HL7、GBK 乱码、危急值、更正(C)、重发去重、ASTM 正常、ASTM 截断、
ASTM 质控(最后一天 1_3s 失控)、Excel(错单位 mg/dL、未映射项目、缺检测时间)。

接入示例:

```bash
# HTTP
curl -X POST "http://localhost:18080/api/ingest?protocol=hl7" --data-binary @testdata/01_hl7_normal.hl7
curl -X POST "http://localhost:18080/api/ingest?protocol=astm" --data-binary @testdata/06_astm_normal.astm
curl -X POST "http://localhost:18080/api/import/excel?source=HOSP-A" -F "file=@testdata/09_excel_hospital.xlsx"

# MLLP (VT \x0b ... FS CR \x1c\x0d) 或 ASTM 帧直接发到 TCP 16600 端口
```

## API 一览

```
POST /api/ingest?protocol=hl7|astm&source=X     接入原始报文
POST /api/import/excel?source=X                 Excel 导入 (multipart file=)
GET  /api/messages  /api/messages/{id}          原始报文列表 / 报文+标准化结果对照
GET|POST /api/analytes                          标准项目
GET|POST /api/refranges                         参考范围
GET|POST|DELETE /api/mappings                   项目映射
GET  /api/results?sample_id=&patient=&analyte=  结果查询
GET  /api/results/{id}/history                  更正历史链
GET  /api/critical?status=                      危急值事件
POST /api/critical/{id}/ack | /close            危急值闭环
GET|POST /api/critical/rules                    危急值规则
GET|POST /api/qc/lots   GET /api/qc/results     质控
GET  /api/stats/tat                             TAT 统计
```

## Excel 模板

表头按名称匹配(支持别名): 样本号、病人ID、姓名、性别、年龄、项目代码、项目名称、
结果、单位、检测时间; 质控行加 质控批号、质控水平。

## 结构

```
cmd/server   服务入口(主数据种子)
cmd/gen      测试报文生成器
internal/parser   HL7/ASTM/Excel 容错解析
internal/engine   标准化流水线: 映射→换算→参考范围→去重/更正→危急值→Westgard
internal/store    SQLite (WAL)
internal/api      REST + MLLP/TCP 监听 + 嵌入式控制台
```

测试: `go test ./...`
