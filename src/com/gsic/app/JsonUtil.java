package com.gsic.app;

/**
 * JSON 小工具（v4.23 重构：从已删的 AgentLoop 迁移而来，
 * 供 importSkillCardJson 等保留代码复用）。
 */
public class JsonUtil {

    /** 从文本中抠出第一个 JSON 对象：取第一个 '{' 到最后一个 '}' 之间解析。 */
    public static org.json.JSONObject extractJson(String text) {
        if (text == null) return null;
        int a = text.indexOf('{');
        int b = text.lastIndexOf('}');
        if (a < 0 || b <= a) return null;
        try {
            return new org.json.JSONObject(text.substring(a, b + 1));
        } catch (Exception e) {
            return null;
        }
    }
}
