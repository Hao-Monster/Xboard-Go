import { expect, test } from "@playwright/test";
import { adminAPIPath, adminEmail, adminEntryPath, adminPassword, expectLoginPage } from "./support";

test("new backend controls work through the real administrator UI", async ({page}) => {
  const errors: string[]=[]; page.on("pageerror",error => errors.push(error.message));
  await page.goto(adminEntryPath); await expectLoginPage(page);
  await page.getByLabel("邮箱",{exact:true}).fill(adminEmail);
  await page.getByLabel("密码",{exact:true}).fill(adminPassword);
  await page.getByRole("button",{name:"登录",exact:true}).click();
  await expect(page.getByRole("navigation",{name:"管理端导航",exact:true})).toBeVisible();
  const name=`Audience ${Date.now()}`;
  const created=await page.evaluate(async ({path,name}) => {
    const token=document.cookie.split("; ").find(item => item.startsWith("xboard_csrf="))?.slice(12) ?? "";
    const response=await fetch(path,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":decodeURIComponent(token)},body:JSON.stringify({name,group_id:null,transfer_enable:1,speed_limit:null,content:"",reset_traffic_method:null,capacity_limit:null,prices:{monthly:100},device_limit:null,tags:[]})});
    return {status:response.status,body:await response.text()};
  },{path:adminAPIPath("/api/v1/admin/plans"),name});
  expect(created.status,created.body).toBe(201);
  await page.goto(`${adminEntryPath}#/finance/plan`);
  await page.getByRole("button",{name:`购买权限：${name}`,exact:true}).click();
  const dialog=page.getByRole("dialog",{name:"套餐购买权限"});
  await expect(dialog.getByLabel("分销商购买范围")).toHaveValue("none");
  await dialog.getByLabel("分销商购买范围").selectOption("all");
  await dialog.getByRole("button",{name:"保存购买权限"}).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button",{name:`购买权限：${name}`,exact:true}).click();
  await expect(dialog.getByLabel("分销商购买范围")).toHaveValue("all");
  await dialog.screenshot({path:test.info().outputPath("plan-audience.png")});
  await dialog.getByRole("button",{name:"取消",exact:true}).click();
  await page.goto(`${adminEntryPath}#/dashboard`);
  await page.getByRole("button",{name:"流量重置记录",exact:true}).click();
  const resets=page.getByRole("dialog",{name:"流量重置记录",exact:true});
  await expect(resets.getByLabel("全局统计")).toContainText("共 0 次");
  await expect(resets.getByText("暂无匹配的重置记录。")).toBeVisible();
  await resets.getByLabel("触发来源").selectOption("manual");
  await resets.getByRole("button",{name:"查询记录"}).click();
  await expect(resets.getByRole("button",{name:"查询记录"})).toBeEnabled();
  await resets.screenshot({path:test.info().outputPath("traffic-reset.png")});
  expect(errors).toEqual([]);
});
