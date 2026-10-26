import { expect, test } from '@playwright/test';
import { loginAs, readMenu } from './fixtures';
import type { Role } from './fixtures';

// 07 §6 routes/menus: visibility for different roles.
const expected: Record<Role, Array<[string, string[]]>> = {
  R_SUPER: [
    ['仪表盘', []],
    ['目录', ['包管理', '分类']],
    ['内容运营', ['合集', '精选位', '术语表', '公告', '文章', '作者']],
    ['版本记录', ['版本列表', '翻译管理']],
    ['运维', ['任务中心', '搜索洞察', '用户反馈', 'Agent 与 AI 日志']],
    ['客户端', ['客户端版本', '下载镜像', '远程配置']],
    ['系统', ['管理员', '角色与权限', '审计日志', 'Agent 接入', 'GitHub 设置', '站点与索引']]
  ],
  R_ADMIN: [
    ['仪表盘', []],
    ['目录', ['包管理', '分类']],
    ['内容运营', ['合集', '精选位', '术语表', '公告', '文章', '作者']],
    ['版本记录', ['版本列表', '翻译管理']],
    ['运维', ['任务中心', '搜索洞察', '用户反馈', 'Agent 与 AI 日志']],
    ['客户端', ['客户端版本', '下载镜像', '远程配置']],
    ['系统', ['审计日志', 'GitHub 设置', '站点与索引']]
  ],
  R_EDITOR: [
    ['仪表盘', []],
    ['目录', ['包管理', '分类']],
    ['内容运营', ['合集', '精选位', '文章']],
    ['版本记录', ['版本列表', '翻译管理']],
    ['运维', ['搜索洞察', '用户反馈']]
  ],
  R_REVIEWER: [
    ['仪表盘', []],
    ['目录', ['包管理']],
    ['版本记录', ['版本列表', '翻译管理']]
  ],
  R_OPS: [
    ['仪表盘', []],
    ['目录', ['包管理']],
    ['版本记录', ['版本列表']],
    ['运维', ['任务中心', '搜索洞察', '用户反馈']],
    ['客户端', ['客户端版本', '下载镜像', '远程配置']]
  ]
};

for (const [role, menu] of Object.entries(expected) as Array<[Role, Array<[string, string[]]>]>) {
  test(`${role} sees menus specified in 07 §6`, async ({ page }) => {
    await loginAs(page, role);
    expect(await readMenu(page)).toEqual(menu);
    // Detail pages and profile do not appear in menus.
    await expect(page.locator('.n-menu').first()).not.toContainText('包详情');
    await expect(page.locator('.n-menu').first()).not.toContainText('个人中心');
  });
}

test('Direct access without the required role shows 403', async ({ page }) => {
  await loginAs(page, 'R_REVIEWER');
  await page.goto('/system/user');
  await page.waitForURL('**/403');
});

test('Details highlight their parent menu; Profile opens from the avatar menu', async ({ page }) => {
  await loginAs(page, 'R_EDITOR');
  await page.goto('/catalog/package-detail/42');
  await expect(page.locator('.n-menu-item-content--selected').first()).toContainText('包管理');
  // Avatar shows the username; click to open its menu.
  await page.getByRole('button', { name: 'r_editor' }).click();
  await page.getByText('个人中心').click();
  await page.waitForURL('**/user-center');
});
