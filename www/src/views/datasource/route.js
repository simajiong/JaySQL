import lazyImport from '@/utils/lazyImport'

// ---------------------------------------------------------------------------
// 数据源管理（一级菜单）
//
// 2026-10-20 菜单结构调整：把「对数据源本身的管理」从原位置整体迁入本模块——
//   * 实例管理：原 系统管理 → 系统配置 → 实例配置（/admin/instance）
//   * 环境管理：原 系统管理 → 系统配置 → 环境管理（/admin/environment）
//   * 访问申请：原 SQL 工作台 → 访问申请（/sql-workbench/access）
//   * 数据源总览：新增（管理侧覆盖概览）
//
// 2026-10-25 二次调整：数据授权（/datasource/grants）已按产品口径回迁到
// 「系统管理 → 权限管理」（www/src/views/admin/route.js 的 view.admin.perms 下），
// 本模块不再注册该入口。注意 view.datasource / 数据源总览 的 permission 里
// 仍保留 system.das.view —— 那是总览页自身对「授权管理者」开放的既有口径，
// 且父级不带该 code 会导致总览对这类用户被父菜单过滤掉，不要顺手删。
//
// 迁移原则：
//   1. 页面组件（.vue）与后端接口零改动，只迁移路由挂载点；
//   2. 路由 name 保持不变（view.admin.instance / view.admin.environment /
//      das.access 等），其余页面里按 name 的 router.push 无需修改、不会失效；
//   3. 路径统一收敛到 /datasource/*，原路径不再注册（无残留入口）。
// ---------------------------------------------------------------------------

const route = {
  name: 'view.datasource',
  path: '/datasource',
  component: lazyImport(() => import('./index.vue')),
  redirect: '/datasource/overview',
  meta: {
    title: '数据源管理',
    icon: 'ClusterOutlined',
    keepAlive: true,
    // 父级菜单可见性 = 任意子项权限命中（permission.js 对数组按 OR 递归判定，这里显式声明同样口径）
    permission: [
      'system.instance.view',
      'system.environment.view',
      'system.das.view',
      'system.inspect.view',
      'monitor.view',
      'backup.task.view',
      'das.access.apply',
      'das.access.approve',
      'das.access.view',
      'datasec.rule.view',
      'datasec.plain.request',
    ],
  },
  children: [
    {
      name: 'datasource.overview',
      path: '/datasource/overview',
      component: lazyImport(() => import('./overview/Overview.vue')),
      meta: {
        title: '数据源总览',
        icon: 'FundProjectionScreenOutlined',
        keepAlive: true,
        // 总览是管理侧视角（实例清单+覆盖情况），与后端 /datasource/overview 的
        // RequireAnyPermission 保持同一集合；不开放给仅持 das.access.apply 的普通成员。
        permission: ['system.instance.view', 'monitor.view', 'backup.task.view', 'system.das.view'],
      },
    },
    {
      name: `view.admin.instance`,
      path: '/datasource/instance',
      component: lazyImport(() => import('../admin/system/instances/InstanceList.vue')),
      meta: { title: '实例管理', icon: 'DatabaseOutlined', keepAlive: true, permission: 'system.instance.view' },
    },
    {
      // 2026-10-21 迁入：原 系统管理 → 系统配置 → SQL规范（/admin/sql-standards）
      // 规范按实例绑定，与实例管理同属「对数据源本身的管理」
      name: `view.admin.sql_standards`,
      path: '/datasource/sql-standards',
      component: lazyImport(() => import('../admin/system/sql-standards/SQLStandardsList.vue')),
      meta: { title: 'SQL 规范', icon: 'CheckCircleOutlined', keepAlive: true, permission: 'system.inspect.view' },
    },
    {
      name: `view.admin.sql_standard_detail`,
      path: '/datasource/sql-standards/:id',
      component: lazyImport(() => import('../admin/system/sql-standards/SQLStandardDetail.vue')),
      meta: { title: 'SQL 规范详情', keepAlive: true, hidden: true, permission: 'system.inspect.view' },
    },
    {
      name: `view.admin.environment`,
      path: '/datasource/environment',
      component: lazyImport(() => import('../admin/system/environments/EnvironmentList.vue')),
      meta: { title: '环境管理', icon: 'ClusterOutlined', keepAlive: true, permission: 'system.environment.view' },
    },
    {
      name: 'das.access',
      path: '/datasource/access',
      component: lazyImport(() => import('../das/access/AccessRequestList.vue')),
      meta: {
        title: '访问申请',
        icon: 'SafetyCertificateOutlined',
        keepAlive: true,
        // 申请人 / 审批人 / 审计视角三种角色都能进页面，
        // 具体能看哪些数据由页面内「与我相关/我的申请/待我审批/全部记录」下拉与后端数据范围收敛。
        permission: ['das.access.apply', 'das.access.approve', 'das.access.view'],
      },
    },
    {
      // 新建申请独立成页：申请要填「数据源 + 粒度 + 库表 + 审批人」，弹窗塞不下，
      // 且提交后需要完整的成功态（单号 + 去详情）而不是一闪而过的 toast。
      name: 'das.access.apply',
      path: '/datasource/access/apply',
      component: lazyImport(() => import('../das/access/AccessApply.vue')),
      meta: {
        title: '新建访问申请',
        hidden: true,
        activeMenu: '/datasource/access',
        permission: 'das.access.apply',
      },
    },
    {
      // 数据安全（脱敏）：规则 / 库表列配置与自动发现 / 明文工单，单页内三 Tab。
      // 查看规则或发起明文申请任一权限命中即可进入，页面内按 hasPermission 收敛操作。
      name: 'datasource.datasec',
      path: '/datasource/datasec',
      component: lazyImport(() => import('./datasec/DataSec.vue')),
      meta: {
        title: '数据安全',
        icon: 'SecurityScanOutlined',
        keepAlive: true,
        permission: ['datasec.rule.view', 'datasec.plain.request'],
      },
    },
    {
      // 详情用工单式布局（标题 + 单号 + 状态 + 操作按钮组），审批 / 转交 / 终止 / 执行授权都在这里落。
      // 必须声明在 /apply 之后：静态段优先，但显式顺序更不容易踩坑。
      name: 'das.access.detail',
      path: '/datasource/access/:request_no',
      component: lazyImport(() => import('../das/access/AccessRequestDetail.vue')),
      meta: {
        title: '访问申请详情',
        hidden: true,
        activeMenu: '/datasource/access',
        permission: ['das.access.apply', 'das.access.approve', 'das.access.view'],
      },
    },
  ],
}

export default route
