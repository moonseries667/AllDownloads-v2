import { describe, expect, it } from "vitest";
import { serviceSummary, type RuntimeFacts } from "../src/components/runtimePresentation";
const facts = (states: string[], state = "RUNNING"): RuntimeFacts => ({ state, services: states.map((state, i) => ({ id: String(i), required: true, state })) });
describe("运行摘要只表达可判定事实", () => {
  it("未知与空服务集合不能成为全部启动", () => {
    expect(serviceSummary().label).toBe("服务状态未知");
    expect(serviceSummary(facts([])).label).toBe("服务状态未知");
    expect(serviceSummary(facts(["ACTIVE", "UNKNOWN"])).label).toBe("服务状态未知");
    expect(serviceSummary(facts(["ACTIVE", "ACTIVE"], "UNKNOWN")).label).toBe("服务状态未知");
  });
  it("区分全部活动、部分活动、失联、异常和未配置", () => {
    expect(serviceSummary(facts(["ACTIVE", "ACTIVE"])).label).toBe("服务全部启动");
    expect(serviceSummary(facts(["ACTIVE", "LOST"], "PARTIAL")).label).toBe("部分服务启动");
    expect(serviceSummary(facts(["LOST"])).label).toBe("服务不可用");
    expect(serviceSummary(facts(["ACTIVE", "ERROR"])).label).toBe("服务异常");
    expect(serviceSummary(facts(["ACTIVE", "NOT_CONFIGURED"])).label).toBe("服务未配置");
  });
});
