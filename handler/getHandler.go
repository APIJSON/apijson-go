package handler

import (
	"encoding/json"
	"fmt"
	"github.com/keepfoo/apijson/db"
	"github.com/keepfoo/apijson/logger"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

func GetHandler(w http.ResponseWriter, r *http.Request) {
	if data, err := ioutil.ReadAll(r.Body); err != nil {
		logger.Error("请求参数有问题: " + err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	} else {
		handleRequestJson(data, w)
	}
}

func handleRequestJson(data []byte, w http.ResponseWriter) {
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(data, &bodyMap); err != nil {
		logger.Error("请求体 JSON 格式有问题: " + err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	respMap := NewSQLParseContext(bodyMap).getResponse()
	w.WriteHeader(respMap["code"].(int))
	if respBody, err := json.Marshal(respMap); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		logger.Debugf("返回数据 %s", string(respBody))
		if _, err = w.Write(respBody); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

type SQLParseContext struct {
	req           map[string]interface{}
	resp          map[string]interface{}
	waitKeys      map[string]bool
	completedKeys map[string]bool
	time          map[string]int64
	end           bool
}

func NewSQLParseContext(bodyMap map[string]interface{}) *SQLParseContext {
	logger.Debugf("NewSQLParseContext %v", bodyMap)
	return &SQLParseContext{
		req:           bodyMap,
		resp:          make(map[string]interface{}),
		waitKeys:      make(map[string]bool),
		completedKeys: make(map[string]bool),
		time:          make(map[string]int64),
	}
}

func (c *SQLParseContext) getResponse() map[string]interface{} {
	startTime := time.Now().Nanosecond()
	for key := range c.req {
		if !c.completedKeys[key] {
			c.parseSQLAndGetResponse(key)
			if c.end {
				return c.resp
			}
		}
	}
	c.resp["time"] = fmt.Sprintf("%dms|%v", (time.Now().Nanosecond()-startTime)/1000000, c.time)
	return c.resp
}

func (c *SQLParseContext) parseSQLAndGetResponse(key string) {
	startTime := time.Now().UnixNano()

	c.waitKeys[key] = true
	logger.Debugf("开始解析 %s", key)
	if c.req[key] == nil {
		c.End(http.StatusBadRequest, "值不能为空, key: "+key)
		return
	}
	if fieldMap, ok := c.req[key].(map[string]interface{}); !ok {
		c.End(http.StatusBadRequest, "值类型不对，只支持 Object 类型")
	} else {
		parseObj := db.SQLParseObject{LoadFunc: c.queryResp}
		if c.end {
			return
		}
		if err := parseObj.From(key, fieldMap); err != nil {
			c.End(http.StatusBadRequest, err.Error())
			return
		} else {
			sql := parseObj.ToSQL()
			logger.Debugf("解析 %s 执行SQL: %s %v", key, sql, parseObj.Values)
			if parseObj.QueryFirst {
				c.resp[key], err = db.QueryOne(sql, parseObj.Values...)
			} else {
				c.resp[key], err = db.QueryAll(sql, parseObj.Values...)
			}
			if err != nil {
				c.End(http.StatusInternalServerError, err.Error())
			} else {
				c.resp["code"] = http.StatusOK
			}
		}
	}
	c.waitKeys[key] = false
	c.time[key] = time.Now().UnixNano() - startTime
}

// 查询已知结果
func (c *SQLParseContext) queryResp(queryString string) interface{} {
	var paths []string
	qs := strings.TrimSpace(queryString)
	if strings.HasPrefix(qs, "/") {
		paths = strings.Split(qs[1:], "/")
	} else {
		paths = strings.Split(queryString, "/")
	}
	var targetValue interface{}
	for _, x := range paths {
		if targetValue == nil {
			if c.waitKeys[x] {
				c.End(http.StatusBadRequest, "关联查询有循环依赖，queryString: "+queryString)
				return nil
			} else if c.completedKeys[x] {
				targetValue = c.resp[x]
			} else {
				c.parseSQLAndGetResponse(x)
				targetValue = c.resp[x]
			}
		} else {
			targetValue = targetValue.(map[string]interface{})[x]
		}
		if targetValue == nil {
			c.End(http.StatusBadRequest, fmt.Sprintf("关联查询未发现相应值，queryString: %s", queryString))
		}
	}
	return targetValue
}

func (c *SQLParseContext) End(code int, msg string) {
	c.resp["code"] = code
	c.resp["msg"] = msg
	c.end = true
	logger.Errorf("发生错误，终止处理, code: %d, msg: %s", code, msg)
}
