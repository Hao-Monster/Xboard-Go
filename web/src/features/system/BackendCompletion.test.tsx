import { StrictMode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PlanAudienceModal } from "../plans/PlanAudienceModal";
import { TrafficResetModal } from "./TrafficResetModal";
import { APIClient, type PlanAudienceAPI, type TrafficResetAPI } from "../../lib/api";

describe("backend completion UI", () => {
  it("saves a selected audience and preserves a failed save for retry", async () => {
    const api: PlanAudienceAPI = {
      getPlanVisibility: vi.fn().mockResolvedValue({plan:{id:8,name:"Plan",customer_visibility:"all",distributor_visibility:"none",customer_users:[],distributor_users:[]}}),
      searchPlanAudienceUsers: vi.fn().mockResolvedValue([{id:42,email:"dealer@example.test"}]),
      savePlanVisibility: vi.fn().mockRejectedValueOnce(new Error("保存失败")).mockResolvedValue(true)
    };
    const saved=vi.fn(); const user=userEvent.setup();
    render(<PlanAudienceModal id={8} api={api} onClose={vi.fn()} onSaved={saved} />);
    await user.selectOptions(await screen.findByLabelText("分销商购买范围"),"selected");
    await user.type(screen.getByLabelText("搜索分销商"),"dealer");
    await user.click(screen.getByRole("button",{name:"搜索名单"}));
    await user.click(await screen.findByRole("button",{name:"添加"}));
    await user.click(screen.getByRole("button",{name:"保存购买权限"}));
    expect(await screen.findByRole("alert")).toHaveTextContent("保存失败");
    expect(saved).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button",{name:"保存购买权限"}));
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(api.savePlanVisibility).toHaveBeenLastCalledWith({plan_id:8,customer_visibility:"all",distributor_visibility:"selected",customer_user_ids:[],distributor_user_ids:[42]});
  });
  it("loads in StrictMode and preserves applied filters when paging", async () => {
    const api: TrafficResetAPI = {
      listGlobalTrafficResets: vi.fn().mockResolvedValue({data:[],pagination:{current_page:1,last_page:2,per_page:20,total:21}}),
      getTrafficResetStats: vi.fn().mockResolvedValue({total_resets:9,auto_resets:0,manual_resets:4,cron_resets:5})
    };
    const user=userEvent.setup();render(<StrictMode><TrafficResetModal api={api} onClose={vi.fn()} /></StrictMode>);
    expect(await screen.findByLabelText("全局统计")).toHaveTextContent("共 9 次");
    await user.type(screen.getByLabelText("用户邮箱"),"abc@example.test");
    await user.click(screen.getByRole("button",{name:"查询记录"}));
    await waitFor(() => expect(api.listGlobalTrafficResets).toHaveBeenLastCalledWith({user_email:"abc@example.test"},1));
    await user.clear(screen.getByLabelText("用户邮箱"));
    await user.click(screen.getByRole("button",{name:"下一页"}));
    await waitFor(() => expect(api.listGlobalTrafficResets).toHaveBeenLastCalledWith({user_email:"abc@example.test"},2));
  });
  it("reads the modern log envelope without dropping pagination", async () => {
    const page={data:[],pagination:{current_page:2,last_page:3,per_page:20,total:42}};
    const fetcher=vi.fn().mockResolvedValue(new Response(JSON.stringify({status:"success",data:page}),{status:200}));
    vi.stubGlobal("fetch",fetcher);
    try {
      expect(await new APIClient("secure-admin-01").listGlobalTrafficResets({user_email:"a+b@example.test"},2)).toEqual(page);
      expect(fetcher.mock.calls[0]?.[0]).toContain("user_email=a%2Bb%40example.test");
    } finally {vi.unstubAllGlobals();}
  });
});
