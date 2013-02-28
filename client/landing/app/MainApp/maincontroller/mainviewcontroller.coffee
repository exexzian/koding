class MainViewController extends KDViewController

  constructor:->

    super

    mainView = @getView()
    @registerSingleton 'mainView', mainView, yes

    mainView.on "SidebarCreated", (sidebar)=>
      @sidebarController = new SidebarController view : sidebar

    # mainView.on "BottomPanelCreated", (bottomPanel)=>
    #   @bottomPanelController = new BottomPanelController view : bottomPanel

    KDView.appendToDOMBody mainView

  loadView:(mainView)->

    mainView.mainTabView.on "MainTabPaneShown", (data)=>
      @mainTabPaneChanged mainView, data.pane

  mainTabPaneChanged:(mainView, pane)->

    {sidebarController}    = @
    sidebar                = sidebarController.getView()
    {navController}        = sidebar
    {type, name, behavior} = pane.getOptions()
    {route}                = KD.getAppOptions name

    @getSingleton("contentDisplayController").emit "ContentDisplaysShouldBeHidden"

    if route is 'Develop'
      @getSingleton('router').handleRoute '/Develop', suppressListeners: yes

    mainView.setViewState behavior

    navController.selectItemByName route

    appManager = @getSingleton "appManager"
    appInstance = appManager.getByView pane.mainView
    appManager.setFrontApp appInstance