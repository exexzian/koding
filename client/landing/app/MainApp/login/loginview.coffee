class LoginView extends KDScrollView

  stop = (event)->
    event.preventDefault()
    event.stopPropagation()

  constructor:(options = {}, data)->

    {entryPoint} = KD.config
    options.cssClass = 'hidden'

    super options, data

    @hidden = yes

    @bindTransitionEnd()

    handler =(route, event)=>
      stop event
      KD.getSingleton('router').handleRoute route, {entryPoint}

    homeHandler                   = handler.bind null, '/'
    loginHandler                  = handler.bind null, '/Login'
    registerHandler               = handler.bind null, '/Register'
    recoverHandler                = handler.bind null, '/Recover'
    resendMailConfirmationHandler = handler.bind null, '/ResendToken'

    @logo = new KDCustomHTMLView
      tagName     : "div"
      cssClass    : "logo"
      partial     : "Koding"
      click       : homeHandler

    @backToLoginLink = new KDCustomHTMLView
      tagName   : "a"
      partial   : "Go ahead and login"
      click     : loginHandler

    @goToRecoverLink = new KDCustomHTMLView
      tagName     : "a"
      partial     : "Recover password"
      testPath    : "landing-recover-password"
      click       : recoverHandler

    @goToRegisterLink = new KDCustomHTMLView
      tagName     : "a"
      partial     : "Register an account"
      click       : registerHandler

    @goToResendMailConfirmationLink = new KDCustomHTMLView
      tagName     : "a"
      partial     : "Resend"
      click       : resendMailConfirmationHandler

    @loginOptions = new LoginOptions
      cssClass : "login-options-holder log"

    @registerOptions = new RegisterOptions
      cssClass : "login-options-holder reg"

    @loginForm = new LoginInlineForm
      cssClass : "login-form"
      testPath : "login-form"
      callback : (formData)=>
        formData.clientId = $.cookie('clientId')
        @doLogin formData
        KD.track "Login", "SignInButtonClicked"

    @registerForm = new RegisterInlineForm
      cssClass : "login-form"
      testPath : "register-form"
      callback : (formData)=>
        @doRegister formData
        KD.track "Login", "RegisterButtonClicked"

    @redeemForm = new RedeemInlineForm
      cssClass : "login-form"
      callback : (formData)=>
        @doRedeem formData
        KD.track "Login", "RedeemButtonClicked"

    @recoverForm = new RecoverInlineForm
      cssClass : "login-form"
      callback : (formData)=>
        @doRecover formData
        KD.track "Login", "RecoverButtonClicked"

    @resendForm= new ResendEmailConfirmationLinkInlineForm
      cssClass : "login-form"
      callback : (formData)=>
        @resendEmailConfirmationToken formData
        KD.track "Login", "ResendEmailConfirmationTokenButtonClicked"

    @resetForm = new ResetInlineForm
      cssClass : "login-form"
      callback : (formData)=>
        formData.clientId = $.cookie('clientId')
        @doReset formData
        KD.track "Login", "ResetButtonClicked"

    @headBanner = new KDCustomHTMLView
      domId    : "invite-recovery-notification-bar"
      cssClass : "invite-recovery-notification-bar hidden"
      partial  : "..."

    KD.getSingleton("mainController").on "landingSidebarClicked", => @unsetClass 'landed'

    setValue = (field, value)=>
      @registerForm[field].input?.setValue value

    mainController = KD.getSingleton "mainController"
    mainController.on "ForeignAuthCompleted", (provider)=>
      isUserLoggedIn = KD.isLoggedIn()
      params = {isUserLoggedIn, provider}

      KD.remote.api.JUser.authenticateWithOauth params, (err, resp)=>
        if err
          showError err
        else
          {account, replacementToken, isNewUser, userInfo} = resp
          if isNewUser
            @animateToForm "register"
            for own field, value of userInfo
              setValue field, value
          else
            if isUserLoggedIn
              mainController.emit "ForeignAuthSuccess.#{provider}"
              new KDNotificationView
                title : "Thanks for linking your #{provider.capitalize()} account!"
            else
              @afterLoginCallback err, {account, replacementToken}

  viewAppended:->

    @setY -KD.getSingleton('windowController').winHeight
    @listenWindowResize()
    @setClass "login-screen login"

    @setTemplate @pistachio()
    @template.update()

  _windowDidResize:->
    if @hidden
      @setY -KD.getSingleton('windowController').winHeight

  pistachio:->
    """
    <div class="flex-wrapper">
      <div class="login-box-header">
        <a class="betatag">beta</a>
        {{> @logo}}
      </div>
      {{> @loginOptions}}
      {{> @registerOptions}}
      <div class="login-form-holder lf">
        {{> @loginForm}}
      </div>
      <div class="login-form-holder rf">
        {{> @registerForm}}
      </div>
      <div class="login-form-holder rdf">
        {{> @redeemForm}}
      </div>
      <div class="login-form-holder rcf">
        {{> @recoverForm}}
      </div>
      <div class="login-form-holder rsf">
        {{> @resetForm}}
      </div>
      <div class="login-form-holder resend-confirmation-form">
        {{> @resendForm}}
      </div>
    </div>
    <div class="login-footer">
      <p class='regLink'>Not a member? {{> @goToRegisterLink}}</p>
      <p class='logLink'>Already a member? {{> @backToLoginLink}}</p>
      <p class='recLink'>Trouble logging in? {{> @goToRecoverLink}}</p>
      <p class='resend-confirmation-link'>Didn't receive confirmation email? {{> @goToResendMailConfirmationLink}}</p>
    </div>
    """

  doReset:({recoveryToken, password, clientId})->
    KD.remote.api.JPasswordRecovery.resetPassword recoveryToken, password, (err, username)=>
      @resetForm.button.hideLoader()
      @resetForm.reset()
      @headBanner.hide()
      @doLogin {username, password, clientId}

  doRecover:(formData)->
    KD.remote.api.JPasswordRecovery.recoverPassword formData['username-or-email'], (err)=>
      @recoverForm.button.hideLoader()
      if err
        new KDNotificationView
          title : "An error occurred: #{err.message}"
      else
        @recoverForm.reset()
        {entryPoint} = KD.config
        KD.getSingleton('router').handleRoute '/Login', {entryPoint}
        new KDNotificationView
          title     : "Check your email"
          content   : "We've sent you a password recovery token."
          duration  : 4500

  resendEmailConfirmationToken:(formData)->
    KD.remote.api.JEmailConfirmation.resetToken formData['username-or-email'], (err)=>
      @resendForm.button.hideLoader()
      if err
        new KDNotificationView
          title : "An error occurred: #{err.message}"
      else
        @resendForm.reset()
        {entryPoint} = KD.config
        KD.getSingleton('router').handleRoute '/Login', {entryPoint}
        new KDNotificationView
          title     : "Check your email"
          content   : "We've sent you a confirmation mail."
          duration  : 4500

  doRegister:(formData)->
    formData.agree = 'on'
    formData.referrer = $.cookie 'referrer'
    @registerForm.notificationsDisabled = yes
    @registerForm.notification?.destroy()

    # we need to close the group channel so we don't receive the cycleChannel event.
    # getting the cycleChannel even for our own MemberAdded can cause a race condition
    # that'll leak a guest account.
    KD.getSingleton('groupsController').groupChannel?.close()

    KD.remote.api.JUser.convert formData, (err, replacementToken)=>
      account = KD.whoami()
      @registerForm.button.hideLoader()

      if err

        {message} = err
        warn "An error occured while registering:", err
        @registerForm.notificationsDisabled = no
        @registerForm.emit "SubmitFailed", message

      else

        $.cookie 'newRegister', yes
        $.cookie 'clientId', replacementToken
        KD.getSingleton('mainController').accountChanged account

        new KDNotificationView
          cssClass  : "login"
          title     : '<span></span>Good to go, Enjoy!'
          # content   : 'Successfully registered!'
          duration  : 2000

        # send information to mixpanel
        KD.track 'UserLogin', 'UserRegistered',
          vendor         : 'mixpanel'
          extra          :
            '$username'  : account.profile.nickname
            '$loginDate' : Date.now()

        KD.getSingleton('router').clear()

        setTimeout =>
          @hide()
          @registerForm.reset()
          @registerForm.button.hideLoader()
        , 1000

        # log to external / TODO: sending account optional if non of track tools use, just delete it
        KD.track "userSignedUp", account

  doLogin:(credentials)->
    credentials.username = credentials.username.toLowerCase()
    KD.remote.api.JUser.login credentials, @afterLoginCallback.bind this

  runExternal = (token)->
    KD.getSingleton("kiteController").run
      kiteName        : "externals"
      method          : "import"
      correlationName : " "
      withArgs        :
        value         : token
        serviceName   : "github"
        userId        : KD.whoami().getId()
      ,
    (err, status)-> console.log "Status of fetching stuff from external: #{status}"

  afterLoginCallback: (err, params={})->
    @loginForm.button.hideLoader()
    {entryPoint} = KD.config
    if err
      showError err
      @loginForm.resetDecoration()
    else
      {account, replacementToken} = params
      $.cookie 'clientId', replacementToken  if replacementToken
      mainController = KD.getSingleton('mainController')
      mainView       = mainController.mainViewController.getView()
      mainController.accountChanged account
      mainView.show()
      mainView.$().css "opacity", 1

      KD.getSingleton('router').handleRoute KD.singletons.router.visitedRoutes.first or '/Activity', {replaceState: yes, entryPoint}
      KD.getSingleton('groupsController').on 'GroupChanged', =>
        new KDNotificationView
          cssClass  : "login"
          title     : "<span></span>Happy Coding!"
          duration  : 2000
        @loginForm.reset()

      new KDNotificationView
        cssClass  : "login"
        title     : "<span></span>Happy Coding!"
        # content   : "Successfully logged in."
        duration  : 2000
      @loginForm.reset()

      @hide()

  doRedeem:({inviteCode})->
    return  unless KD.config.entryPoint?.slug or KD.isLoggedIn()

    KD.remote.cacheable KD.config.entryPoint.slug, (err, [group])=>
      group.redeemInvitation inviteCode, (err)=>
        @redeemForm.button.hideLoader()
        return KD.notify_ err.message or err  if err
        KD.notify_ 'Success!'
        KD.getSingleton('mainController').accountChanged KD.whoami()

  showHeadBanner:(message, callback)->
    @headBannerMsg = message
    @headBanner.updatePartial @headBannerMsg
    @headBanner.unsetClass 'hidden'
    @headBanner.setClass 'show'
    $('body').addClass 'recovery'
    @headBanner.click = callback

  headBannerShowGoBackGroup:(groupTitle)->
    @showHeadBanner "<span>Go Back to</span> #{groupTitle}", =>
      @headBanner.hide()

      $('#group-landing').css 'height', '100%'
      $('#group-landing').css 'opacity', 1

  headBannerShowRecovery:(recoveryToken)->

    @showHeadBanner "Hi, seems like you came here to reclaim your account. <span>Click here when you're ready!</span>", =>
      KD.getSingleton('router').clear '/Recover/Password'
      @headBanner.updatePartial "You can now create a new password for your account"
      @resetForm.addCustomData {recoveryToken}
      @animateToForm "reset"

  headBannerShowInvitation:(invite)->

    @showHeadBanner "Cool! you got an invite! <span>Click here to register your account.</span>", =>
      @headBanner.hide()
      KD.getSingleton('router').clear @getRouteWithEntryPoint('Register')
      $('body').removeClass 'recovery'
      @show =>
        @animateToForm "register"
        @$('.flex-wrapper').addClass 'taller'
        KD.getSingleton('mainController').emit 'InvitationReceived', invite

  hide:(callback)->

    @setY -KD.getSingleton('windowController').winHeight

    cb = =>
      @$('.flex-wrapper').removeClass 'expanded'

      @emit "LoginViewHidden"
      @hidden = yes
      @hideTimer = @utils.wait 2000, => @setClass 'hidden'
      callback?()

    unless @hidden then do cb
    else @once "transitionend", cb

  show:(callback)->

    @utils.killWait @hideTimer
    cb = =>
      @emit "LoginViewShown"
      @hidden = no
      callback?()

    @unsetClass 'hidden'
    @utils.defer =>
      @setY 0
      unless @hidden then do cb
      else @once "transitionend", cb

  click:(event)->
    if $(event.target).is('.login-screen')
      @hide ->
        router = KD.getSingleton('router')
        routed = no
        for route in router.visitedRoutes by -1
          {entryPoint} = KD.config
          routeWithoutEntryPoint =
            if entryPoint?.type is 'group' and entryPoint.slug
            then route.replace "/#{entryPoint.slug}", ''
            else route
          unless routeWithoutEntryPoint in ['/Login', '/Register', '/Recover', '/ResendToken']
            router.handleRoute route
            routed = yes
            break
        router.clear()  unless routed

  animateToForm: (name)->

    @show =>
      switch name
        when "register"
          # @utils.wait 5000, =>
          #   @utils.registerDummyUser()

          KD.remote.api.JUser.isRegistrationEnabled (status)=>
            if status is no
              log "Registrations are disabled!!!"
              @registerForm.$('.main-part').addClass 'hidden'
              @registerForm.disabledNotice.show()
            else
              @registerForm.disabledNotice.hide()
              @registerForm.$('.main-part').removeClass 'hidden'
        when "home"
          parent.notification?.destroy()
          if @headBannerMsg?
            @headBanner.updatePartial @headBannerMsg
            @headBanner.show()

      @unsetClass "register recover login reset home resendEmail"
      @emit "LoginViewAnimated", name
      @setClass name

      switch name
        when "register"
          @registerForm.firstName.input.setFocus()
        when "redeem"
          @redeemForm.inviteCode.input.setFocus()
        when "login"
          @loginForm.username.input.setFocus()
        when "recover"
          @recoverForm.usernameOrEmail.input.setFocus()
        when "resendEmail"
          @resendForm.usernameOrEmail.input.setFocus()

  getRouteWithEntryPoint:(route)->
    {entryPoint} = KD.config
    if entryPoint and entryPoint.slug isnt KD.defaultSlug
      return "/#{entryPoint.slug}/#{route}"
    else
      return "/#{route}"

  showError = (err)->
    if err.message.length > 50
      new KDModalView
        title        : "Something is wrong!"
        width        : 500
        overlay      : yes
        cssClass     : "new-kdmodal"
        content      :
          """
            <div class='modalformline'>
              #{err.message}
            </div>
          """
    else
      new KDNotificationView
        title   : err.message
        duration: 1000
